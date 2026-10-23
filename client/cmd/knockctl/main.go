// Command knockctl is a development tool that talks to a KnockBlock device
// over the ESPHome Native API, using the same client as the tray app. Use it
// to try the device from any PC before the tray app exists, and in
// integration tests. It is not shipped by the installer.
//
//	knockctl [flags] list               list entities
//	knockctl [flags] watch              print state changes until Ctrl-C
//	knockctl [flags] status <id>        set the status (free, muted, mic, camera, call, dnd, offline)
//	knockctl [flags] lang <code>        set the display language (en, it, ...)
//	knockctl [flags] heartbeat          press the heartbeat button
//	knockctl [flags] report <pc> <status> [app]
//	                                    report a PC's status, as the tray app does
//	knockctl detect                     list the Windows consent store entries, the audio
//	                                    sessions and the camera streams: which apps use
//	                                    the mic/camera now
//	knockctl [flags] detect live        print what detection sees every -interval until Ctrl-C
//	knockctl [flags] agent              run the tray app's presence loop without a tray:
//	                                    detect, compute and report until Ctrl-C
//
// The encryption key can also be given in the KNOCKBLOCK_KEY environment
// variable, which keeps it out of the shell history.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"time"

	"github.com/Cecchellone/KnockBlock/client/internal/espapi"
	"github.com/Cecchellone/KnockBlock/client/internal/memdebug"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "knockctl:", err)
		os.Exit(1)
	}
}

func run() error {
	addr := flag.String("addr", "knockblock.local", "device address, host[:port]")
	key := flag.String("key", os.Getenv("KNOCKBLOCK_KEY"), "base64 API encryption key (default $KNOCKBLOCK_KEY; empty = plaintext)")
	timeout := flag.Duration("timeout", 15*time.Second, "timeout for connecting and for commands")
	debugMem := flag.Duration("debug-mem", 0, "log memory statistics at this interval (0 = off)")
	verbose := flag.Bool("v", false, "debug logging")
	host, _ := os.Hostname()
	pc := flag.String("pc", host, "agent: name reported for this PC")
	ignore := flag.String("ignore", "", "agent: comma-separated executables or packages to ignore")
	noApps := flag.Bool("no-app-names", false, "agent: do not send app names")
	interval := flag.Duration("interval", time.Second, "agent: detection poll interval")
	fakeDetect := flag.String("fake-detect", "", "agent: read apps in use from this file instead of the registry (lines: mic|camera <exe or package>)")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: knockctl [flags] list|watch|status <id>|lang <code>|heartbeat|ring|flip on|off|report <pc> <status> [app]|detect|agent\n\nflags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		return errors.New("missing command")
	}

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if *debugMem > 0 {
		go memdebug.Run(ctx, log, *debugMem)
	}

	dialOpts := espapi.Options{
		Address:       *addr,
		EncryptionKey: *key,
		ClientInfo:    "knockctl " + version,
		Logger:        log,
	}
	switch args[0] {
	case "detect":
		if len(args) > 1 && args[1] == "live" {
			return detectLiveCmd(ctx, *interval)
		}
		return detectCmd()
	case "agent":
		return agentCmd(ctx, log, dialOpts, agentFlags{
			pc: *pc, ignore: *ignore, noApps: *noApps, interval: *interval, fakeDetect: *fakeDetect,
		})
	}

	dialCtx, cancel := context.WithTimeout(ctx, *timeout)
	conn, err := espapi.Dial(dialCtx, espapi.Options{
		Address:       *addr,
		EncryptionKey: *key,
		ClientInfo:    "knockctl " + version,
		Logger:        log,
	})
	cancel()
	if err != nil {
		return err
	}
	defer conn.Close()
	major, minor := conn.APIVersion()
	log.Debug("connected", "device", conn.DeviceName(), "server", conn.ServerInfo(), "api", fmt.Sprintf("%d.%d", major, minor))

	cmdCtx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()
	ents, err := conn.ListEntities(cmdCtx)
	if err != nil {
		return err
	}

	switch cmd := args[0]; cmd {
	case "list":
		for _, e := range ents {
			fmt.Printf("%-11s %-18s key=%-10d %q %v%v\n", e.Kind, e.ObjectID, e.Key, e.Name, e.Options, e.Args)
		}
		return nil
	case "watch":
		return watch(ctx, conn, ents)
	case "status", "lang":
		if len(args) != 2 {
			return fmt.Errorf("usage: knockctl %s <value>", cmd)
		}
		objectID := "status"
		if cmd == "lang" {
			objectID = "display_language"
		}
		return setSelect(cmdCtx, conn, ents, objectID, args[1])
	case "heartbeat":
		e, ok := ents.Find(espapi.KindButton, "heartbeat")
		if !ok {
			return errors.New("device has no heartbeat button")
		}
		return conn.PressButton(cmdCtx, e.Key)
	case "ring":
		e, ok := ents.Find(espapi.KindButton, "ring_doorbell")
		if !ok {
			return errors.New("device has no ring_doorbell button (firmware too old)")
		}
		return conn.PressButton(cmdCtx, e.Key)
	case "flip":
		if len(args) != 2 || (args[1] != "on" && args[1] != "off") {
			return errors.New("usage: knockctl flip on|off")
		}
		e, ok := ents.Find(espapi.KindSwitch, "flip_display")
		if !ok {
			return errors.New("device has no flip_display switch")
		}
		return conn.SetSwitch(cmdCtx, e.Key, args[1] == "on")
	case "report":
		if len(args) < 3 || len(args) > 4 {
			return errors.New("usage: knockctl report <pc> <status> [app]")
		}
		e, ok := ents.Find(espapi.KindAction, "report_presence")
		if !ok {
			return errors.New("device has no report_presence action")
		}
		app := ""
		if len(args) == 4 {
			app = args[3]
		}
		return conn.ExecuteAction(cmdCtx, e, map[string]string{"pc": args[1], "status": args[2], "app": app})
	default:
		flag.Usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// setSelect sets a select and waits until the device reports the new value.
func setSelect(ctx context.Context, conn *espapi.Conn, ents espapi.Entities, objectID, value string) error {
	e, ok := ents.Find(espapi.KindSelect, objectID)
	if !ok {
		return fmt.Errorf("device has no %q select", objectID)
	}
	confirmed := make(chan struct{}, 1)
	if err := conn.SubscribeStates(ctx, func(s espapi.State) {
		if s.Key == e.Key && s.Value == value {
			select {
			case confirmed <- struct{}{}:
			default:
			}
		}
	}); err != nil {
		return err
	}
	if err := conn.SetSelect(ctx, e.Key, value); err != nil {
		return err
	}
	select {
	case <-confirmed:
		fmt.Printf("%s = %s\n", objectID, value)
		return nil
	case <-conn.Done():
		return conn.Err()
	case <-ctx.Done():
		return fmt.Errorf("device did not confirm %s = %s (valid options: %v): %w", objectID, value, e.Options, ctx.Err())
	}
}

// watch prints every state change until interrupted or disconnected.
func watch(ctx context.Context, conn *espapi.Conn, ents espapi.Entities) error {
	names := make(map[uint32]string, len(ents))
	for _, e := range ents {
		names[e.Key] = e.ObjectID
	}
	if err := conn.SubscribeStates(ctx, func(s espapi.State) {
		name, ok := names[s.Key]
		if !ok {
			return
		}
		if s.Missing {
			fmt.Printf("%s %s = (no state)\n", time.Now().Format(time.TimeOnly), name)
			return
		}
		fmt.Printf("%s %s = %s\n", time.Now().Format(time.TimeOnly), name, s.Value)
	}); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return nil
	case <-conn.Done():
		return conn.Err()
	}
}
