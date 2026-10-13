// Command screenshot saves a PNG of a web page through headless Chrome (DevTools protocol).
//
//	go run ./tools/screenshot [-w 1280] [-h 800] [-dpr 1.25] [-wait 3.5s] <url> <out.png> [js-step ...]
//
// Each js-step is evaluated in the page after it loads, followed by a 1.5 s pause.
// The Chrome binary comes from $CHROME, or the default location for the platform.
package main

import (
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	width := flag.Int("w", 1280, "viewport width in CSS pixels")
	height := flag.Int("h", 800, "viewport height in CSS pixels")
	// 1.25 at 1280 px gives a 1600 px PNG that stays small; 2 is sharper but about twice the size.
	dpr := flag.Float64("dpr", 1.25, "device pixel ratio")
	wait := flag.Duration("wait", 3500*time.Millisecond, "time for the page to load its data")
	flag.Parse()
	if flag.NArg() < 2 {
		fmt.Fprintln(os.Stderr, "usage: screenshot [flags] <url> <out.png> [js-step ...]")
		os.Exit(2)
	}
	url, out, steps := flag.Arg(0), flag.Arg(1), flag.Args()[2:]

	png, err := capture(url, steps, *width, *height, *dpr, *wait)
	if err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(out, png, 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Println("saved", out)
}

func capture(url string, steps []string, width, height int, dpr float64, wait time.Duration) ([]byte, error) {
	port, err := freePort()
	if err != nil {
		return nil, err
	}
	profile, err := os.MkdirTemp("", "vclu-screenshot-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(profile) }()

	cmd := exec.Command(chromeBinary(), "--headless=new", fmt.Sprintf("--remote-debugging-port=%d", port),
		"--user-data-dir="+profile, "--hide-scrollbars", "about:blank")
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start chrome: %w (set CHROME)", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()

	wsURL, err := pageSocket(port)
	if err != nil {
		return nil, err
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()
	c := &cdp{conn: conn}

	if _, err := c.call("Emulation.setDeviceMetricsOverride", map[string]any{
		"width": width, "height": height, "deviceScaleFactor": dpr, "mobile": false,
	}); err != nil {
		return nil, err
	}
	if _, err := c.call("Page.navigate", map[string]any{"url": url}); err != nil {
		return nil, err
	}
	time.Sleep(wait)

	for _, s := range steps {
		res, err := c.call("Runtime.evaluate", map[string]any{"expression": s, "awaitPromise": true, "returnByValue": true})
		if err != nil {
			return nil, err
		}
		var r struct {
			Result           struct{ Value any }
			ExceptionDetails *struct {
				Text      string
				Exception struct{ Description string }
			}
		}
		_ = json.Unmarshal(res, &r)
		if r.ExceptionDetails != nil {
			msg := r.ExceptionDetails.Exception.Description
			if msg == "" {
				msg = r.ExceptionDetails.Text
			}
			return nil, fmt.Errorf("step %q: %s", s, msg)
		}
		fmt.Printf("step: %v\n", r.Result.Value)
		time.Sleep(1500 * time.Millisecond)
	}

	res, err := c.call("Page.captureScreenshot", map[string]any{"format": "png"})
	if err != nil {
		return nil, err
	}
	var shot struct{ Data string }
	if err := json.Unmarshal(res, &shot); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(shot.Data)
}

// cdp sends DevTools commands one at a time and skips events until the matching reply.
type cdp struct {
	conn *websocket.Conn
	id   int
}

func (c *cdp) call(method string, params map[string]any) (json.RawMessage, error) {
	c.id++
	if err := c.conn.WriteJSON(map[string]any{"id": c.id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for {
		var msg struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := c.conn.ReadJSON(&msg); err != nil {
			return nil, err
		}
		if msg.ID != c.id {
			continue
		}
		if msg.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, msg.Error.Message)
		}
		return msg.Result, nil
	}
}

func pageSocket(port int) (string, error) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(200 * time.Millisecond)
		resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/json", port))
		if err != nil {
			continue
		}
		var targets []struct{ Type, WebSocketDebuggerURL string }
		err = json.NewDecoder(resp.Body).Decode(&targets)
		_ = resp.Body.Close()
		if err != nil {
			continue
		}
		for _, t := range targets {
			if t.Type == "page" {
				return t.WebSocketDebuggerURL, nil
			}
		}
	}
	return "", fmt.Errorf("chrome did not open a page within 10 s")
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func chromeBinary() string {
	if b := os.Getenv("CHROME"); b != "" {
		return b
	}
	switch runtime.GOOS {
	case "darwin":
		return "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	case "windows":
		return `C:\Program Files\Google\Chrome\Application\chrome.exe`
	}
	return "google-chrome"
}
