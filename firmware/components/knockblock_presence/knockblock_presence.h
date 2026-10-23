#pragma once

#include <cstddef>
#include <cstdint>

#include "esphome/components/select/select.h"
#include "esphome/components/text_sensor/text_sensor.h"
#include "esphome/core/component.h"
#include "esphome/core/string_ref.h"

namespace esphome::knockblock_presence {

// Combines the status reported by several sources (PCs, or the manual
// source "" for direct changes of the status select) into the status the
// light shows. See docs/presence.md.
class Presence : public Component {
 public:
  // Longest PC or app name kept, in bytes; longer names are cut at a UTF-8
  // character boundary.
  static constexpr size_t MAX_NAME = 32;
  // Sources tracked at once; when full, the least recently seen is dropped.
  static constexpr size_t MAX_SOURCES = 8;

  void set_status_select(select::Select *s) { this->status_select_ = s; }
  void set_app_sensor(text_sensor::TextSensor *s) { this->app_sensor_ = s; }
  void set_pc_sensor(text_sensor::TextSensor *s) { this->pc_sensor_ = s; }
  void set_devices_sensor(text_sensor::TextSensor *s) { this->devices_sensor_ = s; }
  void set_timeout(uint32_t ms) { this->timeout_ms_ = ms; }

  void setup() override;
  void dump_config() override;
  float get_setup_priority() const override { return setup_priority::DATA; }

  // What a source has in use, for the icons on the display.
  enum Device : uint8_t {
    CALL = 1 << 0,       // an app keeps the microphone open, muted or not
    MIC = 1 << 1,        // the microphone records, not muted
    MIC_MUTED = 1 << 2,  // the microphone is open but muted
    CAMERA = 1 << 3,     // an app streams from the camera
  };

  // Records the status of `source`. Status `offline` removes the source; an
  // unknown status is ignored. The devices in use follow from the status.
  void report(StringRef source, StringRef status, StringRef app);
  // Same, with the devices in use as reported by the PC: a comma-separated
  // list of "call", "mic", "mic_muted" and "camera".
  void report(StringRef source, StringRef status, StringRef app, StringRef devices);
  // Keeps `source` alive without changing it (the heartbeat button).
  void refresh(StringRef source);

  // The devices in use (Device bits) of the source shown.
  uint8_t devices() const { return this->shown_devices_; }
  bool in_call() const { return (this->shown_devices_ & CALL) != 0; }
  // millis() when the source shown entered a call, 0 when not in a call.
  // Kept while the call goes on, whatever its status.
  uint32_t call_start() const { return this->call_start_; }

 protected:
  struct Source {
    bool used{false};
    uint8_t status{0};   // index into STATUSES
    uint8_t devices{0};  // Device bits
    uint32_t last_seen{0};
    char name[MAX_NAME + 1]{};
    char app[MAX_NAME + 1]{};
  };

  Source *find_(StringRef name);
  void expire_();
  void publish_();
  void store_(StringRef source, int index, StringRef app, uint8_t devices);

  select::Select *status_select_{nullptr};
  text_sensor::TextSensor *app_sensor_{nullptr};
  text_sensor::TextSensor *pc_sensor_{nullptr};
  text_sensor::TextSensor *devices_sensor_{nullptr};
  uint32_t timeout_ms_{120000};
  Source sources_[MAX_SOURCES]{};
  bool published_{false};
  uint8_t shown_status_{0};
  uint8_t shown_devices_{0};
  uint32_t call_start_{0};
  char shown_app_[MAX_NAME + 1]{};
  char shown_pc_[MAX_NAME + 1]{};
};

}  // namespace esphome::knockblock_presence
