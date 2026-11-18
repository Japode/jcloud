// `jcloud healthwatch` — say something when a service goes down, instead of
// waiting for somebody to open the site (JCT46, the port of Japode's
// scripts/healthwatch.sh).
//
// Everything it reads was already there: each service declares a healthcheck and
// docker keeps the verdict. What was missing is anything looking at it after the
// deploy finished.
//
//   - It reports a transition, not a state. A service down for hours, said every
//     five minutes, is a message filtered into a folder nobody opens: one line when
//     it breaks, one when it comes back.
//   - It waits out a restart. A container has no verdict for start_period +
//     retries × (interval + timeout), and HEALTH_GRACE_SECONDS (300 by default) is
//     how long a service has to be bad before it counts.
//   - The services come from the compose files, not from what is running: a
//     container that is gone is the loudest failure there is, and a walk over the
//     running ones cannot see it. A need's database is one of them.
//   - It runs on the machine, so it cannot report the machine gone; that is an
//     external uptime check's job.
//
// The line goes to ALERT_WEBHOOK_URL as {"text": …}, which Slack, Discord and
// Mattermost all read, when one is set, and to stdout always. That webhook is the
// one alert channel jcloud has, decided by the owner (JCT46): nothing else is
// routed, retried or escalated. It exits 1 while anything is bad, so the unit
// failing is a second channel with no webhook at all, and 2 when the
// configuration cannot be read, which tells a broken monitor from a broken
// service.
//
// State is one file per service under <pin file's directory>/health, the same
// "good since reported" line the bash wrote, so a server keeps its history across
// the switch.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// healthNow and healthPost are the clock and the webhook, swapped by the tests.
var (
	healthNow  = time.Now
	healthPost = func(url string, body []byte) error {
		client := http.Client{Timeout: 10 * time.Second}
		resp, err := client.Post(url, "application/json", bytes.NewReader(body))
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode >= 300 {
			return fmt.Errorf("HTTP %d", resp.StatusCode)
		}
		return nil
	}
)

// asciiLine reduces a message to printable ASCII. A container's status is text
// nobody here wrote, and on Japode an em dash arrived at the receiver as one
// cp1252 byte and the whole POST was rejected: a monitor that goes quiet over a
// decorative character is worse than one with no decoration.
func asciiLine(s string) string {
	var b strings.Builder
	for _, r := range strings.ReplaceAll(s, "\r", "") {
		switch {
		case r == '\n' || r == '\t' || (r >= 0x20 && r <= 0x7e):
			b.WriteRune(r)
		default:
			b.WriteByte('?')
		}
	}
	return b.String()
}

// Healthwatch runs one pass at root and answers its exit code.
func Healthwatch(root string, run jobRunner, w io.Writer) int {
	die := func(err error) int {
		fmt.Fprintf(w, "\n\033[31mfailed: %v\033[0m\n", err)
		return 2
	}
	p, err := loadJobPlatform(root, run, w)
	if err != nil {
		return die(err)
	}
	webhook := p.env["ALERT_WEBHOOK_URL"]
	grace := 300
	if v, err := strconv.Atoi(p.env["HEALTH_GRACE_SECONDS"]); err == nil && v > 0 {
		grace = v
	}
	notify := func(text string) {
		text = asciiLine(text)
		fmt.Fprintf(w, "    %s\n", text)
		if webhook == "" {
			return
		}
		body, _ := json.Marshal(map[string]string{"text": text})
		if err := healthPost(webhook, body); err != nil {
			fmt.Fprintf(w, "    \033[33mcould not reach ALERT_WEBHOOK_URL: the line above went nowhere\033[0m\n")
		}
	}

	// Read with placeholders for what the files require: this starts nothing, so a
	// fake image is the right image, and a machine before its first deploy still
	// resolves.
	c := p.compose("config", "--services")
	c.Env = p.placeholders()
	out, err := p.out(c)
	services := strings.Fields(out)
	// Reported, not died on: a compose file that does not resolve is what a bad
	// deploy leaves behind, the moment this most needs to speak (JC77).
	if err != nil || len(services) == 0 {
		notify(fmt.Sprintf("DOWN: %s cannot be read: docker compose names no services here, so nothing was checked", p.project))
		return 2
	}

	stateDir := filepath.Join(root, filepath.FromSlash(path.Dir(p.prof.PinFile)), "health")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return die(err)
	}
	now := healthNow().Unix()
	bad := 0
	p.say("%s: %d service(s), grace %ds", p.project, len(services), grace)
	for _, svc := range services {
		state := p.verdict(svc)
		good := 0
		if state == "healthy" {
			good = 1
		} else {
			bad++
		}
		file := filepath.Join(stateDir, svc)
		// What was true last time, and since when. No file is a first run, which
		// says nothing: a monitor whose installation announces every service it can
		// see starts with noise.
		wasGood, since, reported := good, now, 0
		if data, err := os.ReadFile(file); err == nil {
			f := strings.Fields(string(data))
			if len(f) >= 2 {
				wasGood, _ = strconv.Atoi(f[0])
				since, _ = strconv.ParseInt(f[1], 10, 64)
			}
			if len(f) >= 3 {
				reported, _ = strconv.Atoi(f[2])
			}
		}
		write := func(g int, s int64, r int) { os.WriteFile(file, []byte(fmt.Sprintf("%d %d %d\n", g, s, r)), 0o644) }

		switch {
		case good != wasGood:
			// A transition resets the clock. On the way up it is said only if the way
			// down was.
			if good == 1 && reported == 1 {
				notify(fmt.Sprintf("UP: %s/%s is healthy again", p.project, svc))
			}
			write(good, now, 0)
			if good == 1 {
				fmt.Fprintf(w, "    %-12s %s\n", svc, state)
			}
		case good == 1:
			fmt.Fprintf(w, "    %-12s %s\n", svc, state)
			write(good, since, 0)
		default:
			secs := now - since
			if reported == 0 && secs >= int64(grace) {
				notify(fmt.Sprintf("DOWN: %s/%s has been %s for %s, which is longer than a restart takes", p.project, svc, state, humanSecs(secs)))
				reported = 1
			} else if reported == 1 {
				fmt.Fprintf(w, "    %-12s %s for %ds (already reported)\n", svc, state, secs)
			} else {
				fmt.Fprintf(w, "    %-12s %s for %ds, within grace\n", svc, state, secs)
			}
			write(good, since, reported)
		}
	}
	if bad > 0 {
		return 1
	}
	return 0
}

// humanSecs is how long, for somebody who just woke up: "for 0m" is useless.
func humanSecs(s int64) string {
	switch {
	case s < 120:
		return fmt.Sprintf("%ds", s)
	case s < 7200:
		return fmt.Sprintf("%dm", s/60)
	default:
		return fmt.Sprintf("%dh%dm", s/3600, s%3600/60)
	}
}

// verdict is one service in one word: healthy (running, and passing or declaring
// no healthcheck), unhealthy, starting (no verdict yet, which counts as bad so a
// container stuck restarting is eventually reported), whatever docker says
// instead of running, or missing.
func (p *jobPlatform) verdict(svc string) string {
	out, _ := p.out(jobCmd{Name: "docker", Args: []string{"ps", "-aq",
		"--filter", "label=com.docker.compose.project=" + p.project,
		"--filter", "label=com.docker.compose.service=" + svc}})
	ids := strings.Fields(out)
	if len(ids) == 0 {
		return "missing"
	}
	status, err := p.out(jobCmd{Name: "docker", Args: []string{"inspect", "--format", "{{.State.Status}}", ids[0]}})
	if err != nil {
		status = "unknown"
	}
	health, err := p.out(jobCmd{Name: "docker", Args: []string{"inspect", "--format", "{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}", ids[0]}})
	if err != nil {
		health = "none"
	}
	status, health = strings.TrimSpace(status), strings.TrimSpace(health)
	switch {
	case status != "running":
		return status
	case health == "none" || health == "healthy":
		return "healthy"
	default:
		return health
	}
}

// healthwatchCmd is the command.
func healthwatchCmd(args []string) int {
	root := "."
	if len(args) == 2 && args[0] == "--root" {
		root = args[1]
	} else if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "jcloud healthwatch [--root <dir>]")
		return 2
	}
	return Healthwatch(root, runJobCmd, os.Stdout)
}
