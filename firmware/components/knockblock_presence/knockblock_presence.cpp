#include "knockblock_presence.h"

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
  int index = status_index(status);
  if (index < 0) {
    ESP_LOGW(TAG, "Ignoring unknown status '%.*s'", static_cast<int>(status.size()), status.empty() ? "" : status.c_str());
    return;
  }
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
  const char *app = best != nullptr ? best->app : "";
  const char *pc = best != nullptr ? best->name : "";
  if (this->published_ && status == this->shown_status_ && strcmp(app, this->shown_app_) == 0 &&
      strcmp(pc, this->shown_pc_) == 0)
    return;
  this->published_ = true;
  this->shown_status_ = status;
  strcpy(this->shown_app_, app);
  strcpy(this->shown_pc_, pc);
  // App and PC first: the status select's on_value renders all three.
  this->app_sensor_->publish_state(app);
  this->pc_sensor_->publish_state(pc);
  this->status_select_->publish_state(STATUSES[status]);
}

}  // namespace esphome::knockblock_presence
