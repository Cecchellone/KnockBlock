package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Cecchellone/KnockBlock/client/internal/agent"
	"github.com/Cecchellone/KnockBlock/client/internal/detect"
	"github.com/Cecchellone/KnockBlock/client/internal/espapi"
	"github.com/Cecchellone/KnockBlock/client/internal/link"
)

type agentFlags struct {
	pc         string
	ignore     string
	noApps     bool
	interval   time.Duration
	fakeDetect string
}

// agentCmd runs detection and reporting, like the tray app, until ctx is
// done.
func agentCmd(ctx context.Context, log *slog.Logger, dial espapi.Options, f agentFlags) error {
	if f.pc == "" {
		return fmt.Errorf("no PC name: set -pc")
	}
	opts := detect.Options{}
	for _, name := range strings.Split(f.ignore, ",") {
		if name = strings.TrimSpace(name); name != "" {
			opts.Ignore = append(opts.Ignore, name)
		}
	}
	var det detect.Detector
	if f.fakeDetect != "" {
		det = detect.FromFile(f.fakeDetect, opts)
	} else {
		var err error
		if det, err = detect.NewConsentStore(opts); err != nil {
			return fmt.Errorf("%w (use -fake-detect on this platform)", err)
		}
	}
	defer det.Close()

	sup := link.New(link.Options{
		Dial:   link.DialESPHome(dial),
		Logger: log,
		OnState: func(s link.State) {
			switch {
			case s.Connected:
				log.Info("device connected", "addr", dial.Address)
			case s.NeedsConfig:
				log.Error("cannot connect; check the key and the firmware", "err", s.Err)
			default:
				log.Warn("device unreachable; retrying", "err", s.Err)
			}
		},
	})
	a := agent.New(agent.Config{
		PC: f.pc, SendApps: !f.noApps, PollInterval: f.interval,
		Detector: det, Link: sup, Logger: log,
	})

	// The supervisor outlives the agent, so the agent's final `offline`
	// report can still go out.
	supCtx, stopSup := context.WithCancel(context.Background())
	supDone := make(chan struct{})
	go func() { sup.Run(supCtx); close(supDone) }()
	log.Info("agent running; Ctrl-C to stop", "pc", f.pc, "device", dial.Address)
	a.Run(ctx)
	stopSup()
	<-supDone
	return nil
}

// detectCmd prints every consent store entry (Windows only).
func detectCmd() error {
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "DEVICE\tIN USE\tKIND\tSTART\tSTOP\tKEY")
	err := detect.DumpConsentStore(func(dev detect.Device, key string, packaged bool, start, stop uint64) {
		kind := "desktop"
		if packaged {
			kind = "store"
		}
		inUse := ""
		if detect.InUse(start, stop) {
			inUse = "YES"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", dev, inUse, kind, filetime(start), filetime(stop), key)
	})
	if err != nil {
		return err
	}
	return tw.Flush()
}

// filetime formats a Windows FILETIME (100 ns since 1601) as local time.
func filetime(ft uint64) string {
	if ft == 0 {
		return "-"
	}
	const epochDiff = 116444736000000000 // 1601-01-01 to 1970-01-01 in 100 ns
	if ft < epochDiff {
		return fmt.Sprint(ft)
	}
	return time.Unix(0, int64(ft-epochDiff)*100).Format("2006-01-02 15:04:05")
}
