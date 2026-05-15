// refresh-token is a one-shot CLI that re-pairs the Neverskip token by
// running the password + captcha login flow against the real API and writing
// the resulting token where the running service can pick it up.
//
// Two-step protocol, both at https://nskapi.neverskip.com:
//
//  1. POST /parentweb/auth/checkuservalid {userid: <mobile>}
//     → { lg_ref, captcha: "data:image/png;base64,...", lg_stat }
//  2. POST /parentweb/auth/checklogin {lg_ref, pass_val: base64(pwd),
//     cap_rand: <captcha-text>, lg_stat}
//     → { token: <THE TOKEN> }
//
// The captcha is a base64-encoded PNG with 6 alphanumeric characters. We
// save it to a temp file and open it via xdg-open / open / start so the
// human can read and type it.
//
// Password input is masked via golang.org/x/term. The token is written to
// the path given by -o (or $TOKEN_FILE if -o is not set), atomically and
// with mode 0600.
package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"
)

const (
	apiBase   = "https://nskapi.neverskip.com"
	userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
		"(KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36"
)

type checkUserResp struct {
	S bool `json:"S"`
	D struct {
		LgStat  string `json:"lg_stat"`
		LgRef   string `json:"lg_ref"`
		Captcha string `json:"captcha"`
	} `json:"D"`
	F any    `json:"F"`
	M string `json:"M"`
}

type checkLoginResp struct {
	S bool `json:"S"`
	D struct {
		LgStat string `json:"lg_stat"`
		Token  string `json:"token"`
	} `json:"D"`
	F any    `json:"F"`
	M string `json:"M"`
}

func main() {
	out := flag.String("o", os.Getenv("TOKEN_FILE"),
		"write token to this file (default: $TOKEN_FILE; if empty, print to stdout)")
	mobile := flag.String("mobile", os.Getenv("NEVERSKIP_MOBILE"),
		"mobile number used for Neverskip login (or set $NEVERSKIP_MOBILE)")
	flag.Parse()

	if err := run(*mobile, *out); err != nil {
		fmt.Fprintln(os.Stderr, "refresh-token: ", err)
		os.Exit(1)
	}
}

func run(mobile, outPath string) error {
	stdin := bufio.NewReader(os.Stdin)

	if mobile == "" {
		fmt.Print("Mobile number: ")
		line, err := stdin.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read mobile: %w", err)
		}
		mobile = strings.TrimSpace(line)
	}
	if mobile == "" {
		return errors.New("mobile is required (pass -mobile, set $NEVERSKIP_MOBILE, or type it at the prompt)")
	}

	fmt.Fprintln(os.Stderr, "→ POST /parentweb/auth/checkuservalid")
	cv, err := apiCheckUserValid(mobile)
	if err != nil {
		return err
	}
	if !cv.S {
		return fmt.Errorf("checkuservalid rejected: M=%q", cv.M)
	}
	if cv.D.LgRef == "" || cv.D.Captcha == "" {
		return errors.New("checkuservalid: missing lg_ref or captcha in response")
	}

	captchaPath, err := saveCaptchaImage(cv.D.Captcha)
	if err != nil {
		return fmt.Errorf("save captcha: %w", err)
	}
	fmt.Fprintf(os.Stderr, "→ captcha saved to %s\n", captchaPath)
	if err := openImage(captchaPath); err != nil {
		fmt.Fprintf(os.Stderr, "  (couldn't auto-open viewer: %v — open the file manually)\n", err)
	}

	fmt.Print("Captcha (text from the image): ")
	line, err := stdin.ReadString('\n')
	if err != nil {
		return fmt.Errorf("read captcha: %w", err)
	}
	cap := strings.TrimSpace(line)
	if cap == "" {
		return errors.New("captcha is required")
	}

	fmt.Print("Password: ")
	pwBytes, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return fmt.Errorf("read password: %w", err)
	}
	if len(pwBytes) == 0 {
		return errors.New("password is required")
	}
	passVal := base64.StdEncoding.EncodeToString(pwBytes)
	// Best-effort scrub.
	for i := range pwBytes {
		pwBytes[i] = 0
	}

	fmt.Fprintln(os.Stderr, "→ POST /parentweb/auth/checklogin")
	cl, err := apiCheckLogin(cv.D.LgRef, passVal, cap, cv.D.LgStat)
	if err != nil {
		return err
	}
	if !cl.S {
		return fmt.Errorf("checklogin rejected: M=%q", cl.M)
	}
	if cl.D.Token == "" {
		return errors.New("checklogin: response missing token")
	}

	if outPath == "" {
		// stdout — useful for piping into a deploy script.
		fmt.Println(cl.D.Token)
		fmt.Fprintln(os.Stderr, "✓ token printed to stdout (no -o given)")
		return nil
	}
	if err := writeAtomic(outPath, cl.D.Token); err != nil {
		return fmt.Errorf("write token file: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ token written to %s (%d chars)\n", outPath, len(cl.D.Token))
	fmt.Fprintln(os.Stderr, "  the running service will pick up the new token within 5s")
	_ = os.Remove(captchaPath)
	return nil
}

func apiCheckUserValid(mobile string) (*checkUserResp, error) {
	body := map[string]any{"userid": mobile}
	var out checkUserResp
	if err := apiPost("/parentweb/auth/checkuservalid", body, "", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func apiCheckLogin(lgRef, passVal, capRand, lgStat string) (*checkLoginResp, error) {
	body := map[string]any{
		"lg_ref":   lgRef,
		"pass_val": passVal,
		"cap_rand": capRand,
		"lg_stat":  lgStat,
	}
	var out checkLoginResp
	if err := apiPost("/parentweb/auth/checklogin", body, "", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func apiPost(path string, body any, token string, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, apiBase+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "application/json, text/plain, */*")
	req.Header.Set("origin", "https://parent.neverskip.com")
	req.Header.Set("referer", "https://parent.neverskip.com/")
	req.Header.Set("user-agent", userAgent)
	if token != "" {
		req.Header.Set("token", token)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("POST %s: status %d body=%s", path, resp.StatusCode, truncate(string(data), 200))
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode %s: %w (body=%s)", path, err, truncate(string(data), 200))
	}
	return nil
}

func saveCaptchaImage(b64 string) (string, error) {
	// Captcha may arrive as a data URI ("data:image/png;base64,XXX") or
	// raw base64. Strip the prefix if present.
	if i := strings.Index(b64, ","); strings.HasPrefix(b64, "data:") && i >= 0 {
		b64 = b64[i+1:]
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", fmt.Errorf("decode captcha base64: %w", err)
	}
	path := filepath.Join(os.TempDir(), "neverskip-captcha.png")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func openImage(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "linux":
		cmd = exec.Command("xdg-open", path)
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		return fmt.Errorf("no known image-open command for GOOS=%s", runtime.GOOS)
	}
	return cmd.Start()
}

func writeAtomic(path, contents string) error {
	dir := filepath.Dir(path)
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.TrimSpace(contents)+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
