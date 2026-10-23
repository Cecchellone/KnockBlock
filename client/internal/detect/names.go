package detect

// builtinNames maps lower-case executable names and Store package names to
// the friendly names shown on the device (docs/presence.md). Users can add
// or override entries through Options.Names.
var builtinNames = map[string]string{
	// Microsoft Teams: the new client is a Store package with a desktop
	// executable; the classic client is Teams.exe.
	"ms-teams.exe":                           "Teams",
	"teams.exe":                              "Teams",
	"msteams_8wekyb3d8bbwe":                  "Teams",
	"discord.exe":                            "Discord",
	"discordptb.exe":                         "Discord",
	"discordcanary.exe":                      "Discord",
	"zoom.exe":                               "Zoom",
	"slack.exe":                              "Slack",
	"skype.exe":                              "Skype",
	"webex.exe":                              "Webex",
	"ciscowebexstart.exe":                    "Webex",
	"obs64.exe":                              "OBS",
	"obs32.exe":                              "OBS",
	"whatsapp.root.exe":                      "WhatsApp",
	"5319275a.whatsappdesktop_cv1g1gvanyjgm": "WhatsApp",
	"telegram.exe":                           "Telegram",
	"signal.exe":                             "Signal",
	"steamwebhelper.exe":                     "Steam",
	"audacity.exe":                           "Audacity",
	"microsoft.windowscamera_8wekyb3d8bbwe":  "Camera",
	"microsoft.windowssoundrecorder_8wekyb3d8bbwe": "Sound Recorder",
	"microsoftwindows.client.cbs_cw5n1h2txyewy":    "Windows",
	// Browsers: a web call (Meet, Teams on the web) shows as the browser.
	"chrome.exe":  "Chrome",
	"msedge.exe":  "Edge",
	"firefox.exe": "Firefox",
	"brave.exe":   "Brave",
	"opera.exe":   "Opera",
	"vivaldi.exe": "Vivaldi",
}
