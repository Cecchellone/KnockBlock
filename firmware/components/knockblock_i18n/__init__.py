"""Localized status labels for the KnockBlock display (CLAUDE.md §6.2).

Locale files are plain data (``status_id: label``). This component validates
them at ``esphome config`` time and compiles them into a constant table, so
adding a language needs no code changes:

* an unknown status ID is an error, a missing one falls back to the
  fallback language (with a warning);
* the options of ``language_select`` must match the locale files;
* every label character must exist in the fonts listed in ``check_fonts``.
"""

import logging
import re

import esphome.codegen as cg
import esphome.config_validation as cv
from esphome.const import CONF_ID
from esphome.core import ID
import esphome.final_validate as fv

CODEOWNERS = ["@Cecchellone"]

_LOGGER = logging.getLogger(__name__)

# Language-neutral status IDs; keep in sync with the `status` select options
# in common/core.yaml and with client/internal/status.
STATUS_IDS = ("free", "mic", "camera", "call", "dnd", "offline")

CONF_LANGUAGES = "languages"
CONF_FALLBACK = "fallback"
CONF_LANGUAGE_SELECT = "language_select"
CONF_CHECK_FONTS = "check_fonts"

knockblock_i18n_ns = cg.esphome_ns.namespace("knockblock_i18n")
Labels = knockblock_i18n_ns.class_("Labels")
LabelEntry = knockblock_i18n_ns.struct("LabelEntry")

# BCP 47-style codes as used for file names: en, it, pt-BR, ...
_LANGUAGE_CODE = re.compile(r"^[a-z]{2,3}(-[A-Za-z0-9]{2,8})*$")


def _language_code(value):
    value = cv.string_strict(value)
    if not _LANGUAGE_CODE.match(value):
        raise cv.Invalid(
            f"'{value}' is not a valid language code (expected e.g. 'en', 'pt-BR')"
        )
    return value


LOCALE_SCHEMA = cv.Schema(
    {cv.Optional(status_id): cv.string_strict for status_id in STATUS_IDS}
)


def _validate_languages(config):
    languages = config[CONF_LANGUAGES]
    fallback = config[CONF_FALLBACK]
    if fallback not in languages:
        raise cv.Invalid(
            f"Fallback language '{fallback}' has no locale file",
            path=[CONF_FALLBACK],
        )
    missing = [s for s in STATUS_IDS if s not in languages[fallback]]
    if missing:
        raise cv.Invalid(
            f"Fallback language '{fallback}' must define every status: missing "
            + ", ".join(missing),
            path=[CONF_LANGUAGES, fallback],
        )
    for lang, labels in languages.items():
        missing = [s for s in STATUS_IDS if s not in labels]
        if missing:
            _LOGGER.warning(
                "Language '%s' has no label for %s; using '%s' instead",
                lang,
                ", ".join(missing),
                fallback,
            )
    return config


CONFIG_SCHEMA = cv.All(
    cv.Schema(
        {
            cv.GenerateID(): cv.declare_id(Labels),
            cv.Required(CONF_LANGUAGES): cv.Schema(
                {_language_code: LOCALE_SCHEMA}
            ),
            cv.Optional(CONF_FALLBACK, default="en"): _language_code,
            cv.Optional(CONF_LANGUAGE_SELECT): cv.string_strict,
            cv.Optional(CONF_CHECK_FONTS, default=[]): cv.ensure_list(
                cv.string_strict
            ),
        }
    ),
    _validate_languages,
)


def _find_by_id(items, id_):
    for item in items or []:
        item_id = item.get(CONF_ID)
        if item_id is not None and item_id.id == id_:
            return item
    return None


def _font_codepoints(font_config):
    # Imported lazily: esphome_glyphsets ships with ESPHome's font component.
    import esphome_glyphsets as glyphsets

    chars = set("".join(font_config.get("glyphs", [])))
    for extra in font_config.get("extras", []):
        chars |= set("".join(extra.get("glyphs", [])))
    points = {ord(c) for c in chars}
    for glyphset in font_config.get("glyphsets", []):
        points |= set(glyphsets.unicodes_per_glyphset(glyphset))
    return points


def _final_validate(config):
    full_config = fv.full_config.get()
    languages = config[CONF_LANGUAGES]

    if select_id := config.get(CONF_LANGUAGE_SELECT):
        select = _find_by_id(full_config.get("select"), select_id)
        if select is None:
            raise cv.Invalid(
                f"No select with id '{select_id}'", path=[CONF_LANGUAGE_SELECT]
            )
        options = set(select.get("options", []))
        if options != set(languages):
            raise cv.Invalid(
                f"Options of select '{select_id}' ({', '.join(sorted(options))}) "
                f"must match the locale files ({', '.join(sorted(languages))})",
                path=[CONF_LANGUAGE_SELECT],
            )

    for font_id in config[CONF_CHECK_FONTS]:
        font = _find_by_id(full_config.get("font"), font_id)
        if font is None:
            raise cv.Invalid(f"No font with id '{font_id}'", path=[CONF_CHECK_FONTS])
        available = _font_codepoints(font)
        for lang, labels in languages.items():
            missing = sorted(
                {c for text in labels.values() for c in text if ord(c) not in available}
            )
            if missing:
                raise cv.Invalid(
                    f"Font '{font_id}' has no glyph for {''.join(missing)!r} used by "
                    f"language '{lang}'; add the characters to its glyphs",
                    path=[CONF_LANGUAGES, lang],
                )


FINAL_VALIDATE_SCHEMA = _final_validate


async def to_code(config):
    languages = config[CONF_LANGUAGES]
    fallback = config[CONF_FALLBACK]
    entries = [
        cg.StructInitializer(
            LabelEntry,
            ("lang", lang),
            ("status", status_id),
            ("text", labels.get(status_id, languages[fallback][status_id])),
        )
        for lang, labels in languages.items()
        for status_id in STATUS_IDS
    ]
    table = cg.static_const_array(
        ID(f"{config[CONF_ID].id}_table", is_declaration=True, type=LabelEntry),
        cg.ArrayInitializer(*entries, multiline=True),
    )
    cg.new_Pvariable(config[CONF_ID], table, len(entries), fallback)
