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
	"github.com/Cecchellone/KnockBlock/client/internal/status"
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
		Dial:      link.DialESPHome(dial),
		Logger:    log,
		OnConnect: link.WatchDoorbell(func() { log.Info("doorbell rang") }),
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

// detectCmd prints every consent store entry, the audio sessions and the
// camera streams (Windows only).
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
	if err := tw.Flush(); err != nil {
		return err
	}

	// The microphone comes from the audio sessions: who records right now.
	fmt.Println()
	fmt.Fprintln(tw, "AUDIO SESSIONS\t\t\t\t\t\t")
	fmt.Fprintln(tw, "PID\tKIND\tACTIVE\tMUTED\tPEAK\tEXE\tPATH")
	err = detect.DumpAudioSessions(func(s detect.AudioSession) {
		kind := "playback"
		if s.Capture {
			kind = "record"
		}
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%.3f\t%s\t%s\n", s.PID, kind, yes(s.Active), yes(s.Muted), s.Peak, s.Exe, s.Path)
	})
	if err != nil {
		return fmt.Errorf("audio sessions: %w", err)
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	// The camera comes from Media Foundation's camera reports.
	fmt.Println()
	fmt.Fprintln(tw, "CAMERA STREAMS\t\t")
	fmt.Fprintln(tw, "PID\tEXE\tPATH")
	err = detect.DumpCameraStreams(func() { time.Sleep(1500 * time.Millisecond) }, func(pid uint32, exe, path string) {
		fmt.Fprintf(tw, "%d\t%s\t%s\n", pid, exe, path)
	})
	if err != nil {
		return fmt.Errorf("camera streams: %w", err)
	}
	return tw.Flush()
}

// detectLiveCmd prints what the detector sees and the resulting status
// every interval until ctx is done, with the audio sessions of the apps in
// use: to check by hand how an app behaves when muted or with the camera
// off.
func detectLiveCmd(ctx context.Context, interval time.Duration) error {
	det, err := detect.NewConsentStore(detect.Options{})
	if err != nil {
		return err
	}
	defer det.Close()
	var snap detect.Snapshot
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		if err := det.Poll(&snap); err != nil {
			return err
		}
		res := status.Compute(&snap, status.Auto, true)
		var b strings.Builder
		fmt.Fprintf(&b, "%s  %-7s %-14q mic:", time.Now().Format("15:04:05"), res.Status, res.App)
		for _, a := range snap.Mic {
			fmt.Fprintf(&b, " %s", a.Name)
			if a.Muted {
				b.WriteString("(muted)")
			}
		}
		b.WriteString("  camera:")
		for _, a := range snap.Camera {
			fmt.Fprintf(&b, " %s", a.Name)
		}
		b.WriteString("  sessions:")
		err := detect.DumpAudioSessions(func(s detect.AudioSession) {
			if s.Exe == "" || (!s.Active && !s.Capture) {
				return
			}
			kind := "play"
			if s.Capture {
				kind = "rec"
			}
			fmt.Fprintf(&b, " %s/%s", strings.TrimSuffix(s.Exe, ".exe"), kind)
			if !s.Active {
				b.WriteString("(idle)")
			}
			if s.Muted {
				b.WriteString("(muted)")
			}
			if s.Peak >= 0 {
				fmt.Fprintf(&b, "%.2f", s.Peak)
			}
		})
		if err != nil {
			fmt.Fprintf(&b, " error: %v", err)
		}
		fmt.Println(b.String())
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
	}
}

func yes(b bool) string {
	if b {
		return "YES"
	}
	return ""
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
