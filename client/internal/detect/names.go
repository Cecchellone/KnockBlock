package detect

import "strings"

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
	"nvidia broadcast.exe":                   "NVIDIA Broadcast",
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

// builtinIgnore lists programs that record the microphone all the time to
// process it for other apps (noise removal, virtual microphones): the app
// that uses their output is the one in a call. Lower-case executable names.
var builtinIgnore = []string{
	"nvidia broadcast.exe",
	"nvcontainer.exe",
	"rtx voice.exe",
	"voicemeeter.exe",
	"voicemeeterpro.exe",
	"voicemeeter8.exe",
	"voicemeeter8x64.exe",
	"krisp.exe",
}

// browsers are the executables whose window titles are searched for a web
// app (WebAppFromTitle).
var browsers = map[string]bool{
	"chrome.exe": true, "msedge.exe": true, "firefox.exe": true,
	"brave.exe": true, "opera.exe": true, "vivaldi.exe": true,
}

// webApps maps text found in a browser window title (lower-case) to the web
// app's name, most specific first. Titles look like
// "Chat | Microsoft Teams — Mozilla Firefox" or "Meet - abc-defg-hij - Google Chrome".
var webApps = []struct{ match, name string }{
	{"microsoft teams", "Teams"},
	{"google meet", "Meet"},
	{"meet - ", "Meet"},
	{"zoom", "Zoom"},
	{"discord", "Discord"},
	{"slack", "Slack"},
	{"whatsapp", "WhatsApp"},
	{"webex", "Webex"},
	{"jitsi", "Jitsi"},
	{"whereby", "Whereby"},
	{"skype", "Skype"},
	{"messenger", "Messenger"},
	{"telegram", "Telegram"},
}

// WebAppFromTitle returns the web app named in a browser window title, or
// "" if none.
func WebAppFromTitle(title string) string {
	t := strings.ToLower(title)
	for _, w := range webApps {
		if strings.Contains(t, w.match) {
			return w.name
		}
	}
	return ""
}
