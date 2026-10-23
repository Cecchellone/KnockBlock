#pragma once

#include <cstddef>

#include "esphome/core/string_ref.h"

namespace esphome::knockblock_i18n {

// One display label: the text shown for a status ID in a language. The table
// is generated from firmware/locales/*.yaml and lives in flash.
struct LabelEntry {
  const char *lang;
  const char *status;
  const char *text;
};

// Looks up localized status labels. Missing labels were already filled from
// the fallback language at build time; an unknown language falls back too.
class Labels {
 public:
  Labels(const LabelEntry *table, size_t size, const char *fallback)
      : table_(table), size_(size), fallback_(fallback) {}

  // Returns the label for `status` in `lang`, the fallback-language label if
  // `lang` is unknown, or an empty string if `status` is unknown.
  const char *get(StringRef lang, StringRef status) const {
    const char *fallback_text = "";
    for (size_t i = 0; i < this->size_; i++) {
      const LabelEntry &entry = this->table_[i];
      if (!(status == entry.status))
        continue;
      if (lang == entry.lang)
        return entry.text;
      if (StringRef(this->fallback_) == entry.lang)
        fallback_text = entry.text;
    }
    return fallback_text;
  }

 protected:
  const LabelEntry *table_;
  size_t size_;
  const char *fallback_;
};

}  // namespace esphome::knockblock_i18n
