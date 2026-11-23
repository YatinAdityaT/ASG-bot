package relay

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	announcementInterval = 2 * time.Minute
	tunnelRestartDelay   = 15 * time.Second
)

var tunnelURLPattern = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

func RunPhoneTunnel(
	ctx context.Context,
	apiToken string,
	localAPIURL string,
	logOutput io.Writer,
) error {
	settings, err := Load(RoleTermux, apiToken)
	if err != nil {
		return err
	}
	if logOutput == nil {
		logOutput = io.Discard
	}
	for ctx.Err() == nil {
		err = runTunnelProcess(ctx, settings, localAPIURL, logOutput)
		if ctx.Err() != nil {
			return nil
		}
		fmt.Fprintf(logOutput, "Meet relay tunnel stopped: %v; retrying\n", err)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(tunnelRestartDelay):
		}
	}
	return nil
}

func runTunnelProcess(
	ctx context.Context,
	settings Settings,
	localAPIURL string,
	logOutput io.Writer,
) error {
	command := exec.CommandContext(
		ctx,
		"cloudflared",
		"tunnel",
		"--no-autoupdate",
		"--url",
		localAPIURL,
		"--loglevel",
		"info",
	)
	stdout, err := command.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}

	lines := make(chan string, 32)
	var scanners sync.WaitGroup
	scanners.Add(2)
	for _, reader := range []io.Reader{stdout, stderr} {
		go func(reader io.Reader) {
			defer scanners.Done()
			scanner := bufio.NewScanner(reader)
			for scanner.Scan() {
				select {
				case lines <- scanner.Text():
				case <-ctx.Done():
					return
				}
			}
		}(reader)
	}
	go func() {
		scanners.Wait()
		close(lines)
	}()

	processDone := make(chan error, 1)
	go func() {
		processDone <- command.Wait()
	}()

	var publicURL string
	var ticker *time.Ticker
	var ticks <-chan time.Time
	defer func() {
		if ticker != nil {
			ticker.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-processDone:
			if err == nil {
				return fmt.Errorf("cloudflared exited unexpectedly")
			}
			return err
		case line, ok := <-lines:
			if !ok {
				lines = nil
				continue
			}
			if publicURL != "" {
				continue
			}
			publicURL = tunnelURLPattern.FindString(strings.ToLower(line))
			if publicURL == "" {
				continue
			}
			fmt.Fprintln(logOutput, "Meet relay tunnel is online.")
			if err := publishAnnouncement(ctx, settings, publicURL); err != nil {
				fmt.Fprintf(logOutput, "Meet relay discovery publish failed: %v\n", err)
			}
			ticker = time.NewTicker(announcementInterval)
			ticks = ticker.C
		case <-ticks:
			if err := publishAnnouncement(ctx, settings, publicURL); err != nil {
				fmt.Fprintf(logOutput, "Meet relay discovery refresh failed: %v\n", err)
			}
		}
	}
}

func publishAnnouncement(
	ctx context.Context,
	settings Settings,
	publicURL string,
) error {
	message, err := settings.SealAnnouncement(Announcement{
		URL:       publicURL,
		Published: time.Now().UTC(),
	})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		settings.PublishURL(),
		bytes.NewBufferString(message),
	)
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "text/plain; charset=utf-8")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("ntfy returned %s", response.Status)
	}
	return nil
}
