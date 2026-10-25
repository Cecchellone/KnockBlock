"""Combines the status reported by several PCs into the one shown by the
light (docs/presence.md).

Each source (a PC, or "manual" for changes made directly on the `status`
select) reports a status ID, the app causing it and, from PC apps that
know them, the devices in use (call, microphone, camera) for the icons on
the display. Sources expire after
`timeout` without a new report, so a PC that shuts down cannot leave the
light on "call". The light shows the strongest live status
(call > camera > mic > dnd > muted > free), with the app and PC of the source that
causes it; with no live source it shows `offline`.
"""

import esphome.codegen as cg
from esphome.components import select, text_sensor
import esphome.config_validation as cv
from esphome.const import CONF_ID, CONF_TIMEOUT

CODEOWNERS = ["@Cecchellone"]
DEPENDENCIES = ["select"]

CONF_STATUS_SELECT = "status_select"
CONF_APP_SENSOR = "app_sensor"
CONF_PC_SENSOR = "pc_sensor"
CONF_DEVICES_SENSOR = "devices_sensor"

knockblock_presence_ns = cg.esphome_ns.namespace("knockblock_presence")
Presence = knockblock_presence_ns.class_("Presence", cg.Component)

CONFIG_SCHEMA = cv.Schema(
    {
        cv.GenerateID(): cv.declare_id(Presence),
        cv.Required(CONF_STATUS_SELECT): cv.use_id(select.Select),
        cv.Required(CONF_APP_SENSOR): cv.use_id(text_sensor.TextSensor),
        cv.Required(CONF_PC_SENSOR): cv.use_id(text_sensor.TextSensor),
        cv.Optional(CONF_DEVICES_SENSOR): cv.use_id(text_sensor.TextSensor),
        cv.Optional(CONF_TIMEOUT, default="120s"): cv.All(
            cv.positive_time_period_milliseconds,
            cv.Range(min=cv.TimePeriod(seconds=5)),
        ),
    }
).extend(cv.COMPONENT_SCHEMA)


async def to_code(config):
    var = cg.new_Pvariable(config[CONF_ID])
    await cg.register_component(var, config)
    cg.add(var.set_status_select(await cg.get_variable(config[CONF_STATUS_SELECT])))
    cg.add(var.set_app_sensor(await cg.get_variable(config[CONF_APP_SENSOR])))
    cg.add(var.set_pc_sensor(await cg.get_variable(config[CONF_PC_SENSOR])))
    if CONF_DEVICES_SENSOR in config:
        cg.add(
            var.set_devices_sensor(await cg.get_variable(config[CONF_DEVICES_SENSOR]))
        )
    cg.add(var.set_timeout(config[CONF_TIMEOUT].total_milliseconds))
