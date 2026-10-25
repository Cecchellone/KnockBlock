#include "knockblock_presence.h"

#include <algorithm>
#include <cstdio>
#include <cctype>
#include <cstring>
#include <string>

#include "esphome/core/hal.h"
#include "esphome/core/log.h"

namespace esphome::knockblock_presence {

static const char *const TAG = "knockblock_presence";

// Status IDs from weakest to strongest; index 0 (offline) means "no live
// source". Keep in sync with the `status` select options in common/core.yaml.
static const char *const STATUSES[] = {"offline", "free", "muted", "dnd", "night", "mic", "camera", "call"};
static constexpr uint8_t OFFLINE = 0;
static constexpr uint8_t NUM_STATUSES = sizeof(STATUSES) / sizeof(STATUSES[0]);

// Indices of STATUSES, for the devices implied by a status.
static constexpr int S_MUTED = 2, S_MIC = 5, S_CAMERA = 6, S_CALL = 7;

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

// Copies src into dst (size max+1), cut at a UTF-8 character boundary.
static void copy_cut(char *dst, StringRef src, size_t max) {
  size_t n = src.size();
  if (n > max) {
    n = max;
    while (n > 0 && (static_cast<uint8_t>(src.c_str()[n]) & 0xC0) == 0x80)
      n--;
  }
  if (n > 0)
    memcpy(dst, src.c_str(), n);
  dst[n] = '\0';
}

uint32_t Presence::parse_color(StringRef text) {
  std::string s(text.c_str(), text.size());
  while (!s.empty() && s.back() == ' ')
    s.pop_back();
  while (!s.empty() && s.front() == ' ')
    s.erase(0, 1);
  for (auto &ch : s)
    ch = static_cast<char>(tolower(static_cast<unsigned char>(ch)));
  static const struct {
    const char *name;
    uint32_t rgb;
  } NAMES[] = {
      {"red", 0xE00000},   {"orange", 0xFF6000}, {"yellow", 0xFFC000}, {"green", 0x00B050},
      {"cyan", 0x00A0E0},  {"blue", 0x0040E0},   {"purple", 0x8000C0}, {"pink", 0xFF40A0},
      {"white", 0xFFFFFF}, {"black", 0x000000},  {"grey", 0x303030},   {"gray", 0x303030},
  };
  for (const auto &n : NAMES) {
    if (s == n.name)
      return n.rgb;
  }
  if (!s.empty() && s[0] == '#')
    s.erase(0, 1);
  if (s.size() != 6)
    return NO_COLOR;
  uint32_t rgb = 0;
  for (char ch : s) {
    int v;
    if (ch >= '0' && ch <= '9')
      v = ch - '0';
    else if (ch >= 'a' && ch <= 'f')
      v = ch - 'a' + 10;
    else
      return NO_COLOR;
    rgb = rgb << 4 | static_cast<uint32_t>(v);
  }
  return rgb;
}

uint32_t Presence::message_duration_ms(const std::string &text) {
  uint32_t chars = 0;
  for (unsigned char ch : text) {
    if ((ch & 0xC0) != 0x80)  // count characters, not UTF-8 bytes
      chars++;
  }
  return std::min<uint32_t>(std::max<uint32_t>(4000 + 200 * chars, 5000), 30000);
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

void Presence::report_custom(StringRef source, StringRef status, StringRef app, StringRef devices, StringRef label,
                             StringRef color, int32_t duration_s) {
  const int index = status_index(status);
  if (index < 0) {
    ESP_LOGW(TAG, "Ignoring unknown status '%.*s'", static_cast<int>(status.size()), status.empty() ? "" : status.c_str());
    return;
  }
  Source *s = this->store_(source, index, app, devices.empty() ? devices_of(index) : parse_devices(devices), false);
  if (s == nullptr)
    return;  // offline
  copy_cut(s->label, label, MAX_LABEL);
  s->color = parse_color(color);
  s->lifetime_ms = duration_s <= 0 ? KEEP : static_cast<uint32_t>(duration_s) * 1000;
  this->publish_();
}

Presence::Source *Presence::store_(StringRef source, int index, StringRef app, uint8_t devices, bool publish) {
  Source *s = this->find_(source);
  if (index == OFFLINE) {
    if (s != nullptr)
      s->used = false;
    this->publish_();
    return nullptr;
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
  // A plain report has the status' own label and colour and the default
  // lifetime; report_custom sets them after.
  s->label[0] = '\0';
  s->color = NO_COLOR;
  s->lifetime_ms = 0;
  if (publish)
    this->publish_();
  return s;
}

void Presence::refresh(StringRef source) {
  if (Source *s = this->find_(source); s != nullptr)
    s->last_seen = millis();
}

void Presence::expire_() {
  const uint32_t now = millis();
  bool changed = false;
  for (auto &s : this->sources_) {
    const uint32_t lifetime = s.lifetime_ms == 0 ? this->timeout_ms_ : s.lifetime_ms;
    if (s.used && lifetime != KEEP && now - s.last_seen >= lifetime) {
      ESP_LOGW(TAG, "No report from '%s' for %" PRIu32 " ms; dropping it", s.name[0] ? s.name : "manual", lifetime);
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
  // Sources named "~…" are the light's own switches (night mode, phone
  // call): no PC to show.
  const char *pc = best != nullptr && best->name[0] != '~' ? best->name : "";
  const char *label = best != nullptr ? best->label : "";
  const uint32_t color = best != nullptr ? best->color : NO_COLOR;
  // The call timer runs while the source shown is in a call, so a call
  // that goes from muted to camera keeps its start.
  if ((devices & Presence::CALL) == 0)
    this->call_start_ = 0;
  else if (this->call_start_ == 0)
    this->call_start_ = std::max<uint32_t>(millis(), 1);
  if (this->published_ && status == this->shown_status_ && devices == this->shown_devices_ &&
      strcmp(app, this->shown_app_) == 0 && strcmp(pc, this->shown_pc_) == 0 &&
      strcmp(label, this->shown_label_) == 0 && color == this->shown_color_)
    return;
  this->published_ = true;
  this->shown_status_ = status;
  this->shown_devices_ = devices;
  strcpy(this->shown_app_, app);
  strcpy(this->shown_pc_, pc);
  strcpy(this->shown_label_, label);
  this->shown_color_ = color;
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
