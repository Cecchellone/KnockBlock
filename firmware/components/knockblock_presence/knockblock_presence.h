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
  void set_timeout(uint32_t ms) { this->timeout_ms_ = ms; }

  void setup() override;
  void dump_config() override;
  float get_setup_priority() const override { return setup_priority::DATA; }

  // Records the status of `source`. Status `offline` removes the source; an
  // unknown status is ignored.
  void report(StringRef source, StringRef status, StringRef app);
  // Keeps `source` alive without changing it (the heartbeat button).
  void refresh(StringRef source);

 protected:
  struct Source {
    bool used{false};
    uint8_t status{0};  // index into STATUSES
    uint32_t last_seen{0};
    char name[MAX_NAME + 1]{};
    char app[MAX_NAME + 1]{};
  };

  Source *find_(StringRef name);
  void expire_();
  void publish_();

  select::Select *status_select_{nullptr};
  text_sensor::TextSensor *app_sensor_{nullptr};
  text_sensor::TextSensor *pc_sensor_{nullptr};
  uint32_t timeout_ms_{120000};
  Source sources_[MAX_SOURCES]{};
  bool published_{false};
  uint8_t shown_status_{0};
  char shown_app_[MAX_NAME + 1]{};
  char shown_pc_[MAX_NAME + 1]{};
};

}  // namespace esphome::knockblock_presence
