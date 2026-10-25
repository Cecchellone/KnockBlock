#include "knockblock_presence.h"

#include <algorithm>
#include <cstdio>
#include <cstring>

#include "esphome/core/hal.h"
#include "esphome/core/log.h"

namespace esphome::knockblock_presence {

static const char *const TAG = "knockblock_presence";

// Status IDs from weakest to strongest; index 0 (offline) means "no live
// source". Keep in sync with the `status` select options in common/core.yaml.
static const char *const STATUSES[] = {"offline", "free", "muted", "dnd", "mic", "camera", "call"};
static constexpr uint8_t OFFLINE = 0;
static constexpr uint8_t NUM_STATUSES = sizeof(STATUSES) / sizeof(STATUSES[0]);

// Indices of STATUSES, for the devices implied by a status.
static constexpr int S_MUTED = 2, S_MIC = 4, S_CAMERA = 5, S_CALL = 6;

// Devices implied by a status, for reports that do not list them (old PC
// apps, the manual source).
static uint8_t devices_of(int status) {
  switch (status) {
    case S_MUTED:
      return Presence::CALL | Presence::MIC_MUTED;
    case S_MIC:
      return Presence::MIC;
    case S_CAMERA:
      return Presence::CAMERA;
    case S_CALL:
      return Presence::CALL | Presence::MIC | Presence::CAMERA;
    default:
      return 0;
  }
}

// Parses "call,mic_muted,camera"; unknown names are ignored.
static uint8_t parse_devices(StringRef list) {
  uint8_t bits = 0;
  const char *p = list.c_str();
  const char *end = p + list.size();
  while (p < end) {
    const char *comma = static_cast<const char *>(memchr(p, ',', end - p));
    const char *stop = comma != nullptr ? comma : end;
    const size_t n = stop - p;
    if (n == 4 && memcmp(p, "call", 4) == 0)
      bits |= Presence::CALL;
    else if (n == 3 && memcmp(p, "mic", 3) == 0)
      bits |= Presence::MIC;
    else if (n == 9 && memcmp(p, "mic_muted", 9) == 0)
      bits |= Presence::MIC_MUTED;
    else if (n == 6 && memcmp(p, "camera", 6) == 0)
      bits |= Presence::CAMERA;
    p = stop + 1;
  }
  return bits;
}

// The devices sensor's text: the same list as reported.
static void format_devices(uint8_t bits, char *out, size_t size) {
  static const struct {
    uint8_t bit;
    const char *name;
  } NAMES[] = {
      {Presence::CALL, "call"},
      {Presence::MIC, "mic"},
      {Presence::MIC_MUTED, "mic_muted"},
      {Presence::CAMERA, "camera"},
  };
  size_t len = 0;
  out[0] = '\0';
  for (const auto &n : NAMES) {
    if ((bits & n.bit) == 0)
      continue;
    len += snprintf(out + len, size - len, "%s%s", len == 0 ? "" : ",", n.name);
    if (len >= size)
      break;
  }
}

static int status_index(StringRef status) {
  for (uint8_t i = 0; i < NUM_STATUSES; i++) {
    if (status == STATUSES[i])
      return i;
  }
  return -1;
}

// Length of `src` cut to Presence::MAX_NAME bytes without splitting a UTF-8
// character, so a long name always maps to the same stored name.
static size_t name_len(StringRef src) {
  size_t n = src.size();
  if (n > Presence::MAX_NAME) {
    n = Presence::MAX_NAME;
    while (n > 0 && (static_cast<uint8_t>(src.c_str()[n]) & 0xC0) == 0x80)
      n--;
  }
  return n;
}

static void copy_name(char *dst, StringRef src) {
  const size_t n = name_len(src);
  if (n > 0)
    memcpy(dst, src.c_str(), n);
  dst[n] = '\0';
}

void Presence::setup() {
  this->set_interval("expire", 1000, [this]() { this->expire_(); });
  this->publish_();
}

void Presence::dump_config() {
  ESP_LOGCONFIG(TAG,
                "KnockBlock presence:\n"
                "  Timeout: %" PRIu32 " ms\n"
                "  Max sources: %u",
                this->timeout_ms_, static_cast<unsigned>(MAX_SOURCES));
}

Presence::Source *Presence::find_(StringRef name) {
  const size_t n = name_len(name);
  for (auto &s : this->sources_) {
    if (s.used && strlen(s.name) == n && (n == 0 || memcmp(s.name, name.c_str(), n) == 0))
      return &s;
  }
  return nullptr;
}

void Presence::report(StringRef source, StringRef status, StringRef app) {
  const int index = status_index(status);
  if (index < 0) {
    ESP_LOGW(TAG, "Ignoring unknown status '%.*s'", static_cast<int>(status.size()), status.empty() ? "" : status.c_str());
    return;
  }
  this->store_(source, index, app, devices_of(index));
}

void Presence::report(StringRef source, StringRef status, StringRef app, StringRef devices) {
  const int index = status_index(status);
  if (index < 0) {
    ESP_LOGW(TAG, "Ignoring unknown status '%.*s'", static_cast<int>(status.size()), status.empty() ? "" : status.c_str());
    return;
  }
  this->store_(source, index, app, parse_devices(devices));
}

void Presence::store_(StringRef source, int index, StringRef app, uint8_t devices) {
  Source *s = this->find_(source);
  if (index == OFFLINE) {
    if (s != nullptr)
      s->used = false;
    this->publish_();
    return;
  }
  if (s == nullptr) {
    // Reuse a free slot, or the least recently seen one.
    s = &this->sources_[0];
    for (auto &candidate : this->sources_) {
      if (!candidate.used) {
        s = &candidate;
        break;
      }
      if (candidate.last_seen - s->last_seen > 0x80000000UL)  // candidate older (wrap-safe)
        s = &candidate;
    }
    *s = Source{};
    s->used = true;
    copy_name(s->name, source);
  }
  s->status = static_cast<uint8_t>(index);
  s->devices = devices;
  copy_name(s->app, app);
  s->last_seen = millis();
  this->publish_();
}

void Presence::refresh(StringRef source) {
  if (Source *s = this->find_(source); s != nullptr)
    s->last_seen = millis();
}

void Presence::expire_() {
  const uint32_t now = millis();
  bool changed = false;
  for (auto &s : this->sources_) {
    if (s.used && now - s.last_seen >= this->timeout_ms_) {
      ESP_LOGW(TAG, "No report from '%s' for %" PRIu32 " ms; dropping it", s.name[0] ? s.name : "manual",
               this->timeout_ms_);
      s.used = false;
      changed = true;
    }
  }
  if (changed)
    this->publish_();
}

void Presence::publish_() {
  // Strongest status wins; on a tie, the most recent report.
  const Source *best = nullptr;
  for (const auto &s : this->sources_) {
    if (!s.used)
      continue;
    if (best == nullptr || s.status > best->status ||
        (s.status == best->status && s.last_seen - best->last_seen < 0x80000000UL))
      best = &s;
  }
  const uint8_t status = best != nullptr ? best->status : OFFLINE;
  const uint8_t devices = best != nullptr ? best->devices : 0;
  const char *app = best != nullptr ? best->app : "";
  const char *pc = best != nullptr ? best->name : "";
  // The call timer runs while the source shown is in a call, so a call
  // that goes from muted to camera keeps its start.
  if ((devices & Presence::CALL) == 0)
    this->call_start_ = 0;
  else if (this->call_start_ == 0)
    this->call_start_ = std::max<uint32_t>(millis(), 1);
  if (this->published_ && status == this->shown_status_ && devices == this->shown_devices_ &&
      strcmp(app, this->shown_app_) == 0 && strcmp(pc, this->shown_pc_) == 0)
    return;
  this->published_ = true;
  this->shown_status_ = status;
  this->shown_devices_ = devices;
  strcpy(this->shown_app_, app);
  strcpy(this->shown_pc_, pc);
  // App and PC first: the status select's on_value renders all three.
  this->app_sensor_->publish_state(app);
  this->pc_sensor_->publish_state(pc);
  if (this->devices_sensor_ != nullptr) {
    char text[32];
    format_devices(devices, text, sizeof(text));
    this->devices_sensor_->publish_state(text);
  }
  this->status_select_->publish_state(STATUSES[status]);
}

}  // namespace esphome::knockblock_presence
