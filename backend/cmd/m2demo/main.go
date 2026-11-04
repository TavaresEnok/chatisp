// Command m2demo runs an explicitly local, simulated Meta text endpoint.
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

func handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		if r.Method != http.MethodPost || len(parts) != 3 || parts[0] != "v23.0" || parts[1] == "" || parts[2] != "messages" || r.Header.Get("User-Agent") != "chatisp-metacloud-simulation" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Product string `json:"messaging_product"`
			To      string `json:"to"`
			Type    string `json:"type"`
			Text    struct {
				Body string `json:"body"`
			} `json:"text"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil || body.Product != "whatsapp" || body.To == "" || body.Type != "text" || body.Text.Body == "" {
			http.Error(w, "requisição de simulação inválida", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"messaging_product": "whatsapp", "messages": []map[string]string{{"id": "wamid.simulation." + uuid.NewString()}}})
	})
}

func run(args []string) error {
	if len(args) > 0 && args[0] == "m7" {
		return m7Demo(args[1:])
	}
	if len(args) > 0 && args[0] == "m4b" {
		return m4bDemo(args[1:])
	}
	if len(args) > 0 && args[0] == "m4a" {
		return m4aDemo(args[1:])
	}
	if len(args) > 0 && args[0] == "m3b" {
		return m3bDemo(args[1:])
	}
	if len(args) > 0 && args[0] == "demo" {
		return demo(args[1:])
	}
	if len(args) > 0 && args[0] == "receive" {
		return receive(args[1:])
	}
	flags := flag.NewFlagSet("m2demo", flag.ContinueOnError)
	port := flags.Int("port", 18081, "porta local do servidor Meta simulado")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *port < 1 || *port > 65535 {
		return errors.New("porta ou argumentos inválidos")
	}
	addr := net.JoinHostPort("127.0.0.1", fmt.Sprint(*port))
	log.Printf("SIMULAÇÃO Meta local em http://%s; nenhuma chamada externa", addr)
	return (&http.Server{Addr: addr, Handler: handler(), ReadHeaderTimeout: 5 * time.Second}).ListenAndServe()
}

// receive injects one visibly simulated message only on explicit invocation.
func receive(args []string) error {
	flags := flag.NewFlagSet("receive", flag.ContinueOnError)
	base := flags.String("api", "http://127.0.0.1:8080", "URL local da API")
	app := flags.String("app", "", "App ID da caixa de simulação")
	secret := flags.String("secret", "", "App Secret exclusivamente de simulação")
	waba := flags.String("waba", "", "WABA ID de simulação")
	phone := flags.String("phone", "", "Phone Number ID de simulação")
	from := flags.String("from", "5511988887777", "identidade simulada do cliente")
	body := flags.String("text", "Olá, esta é uma mensagem de simulação.", "texto simulado")
	if err := flags.Parse(args); err != nil {
		return err
	}
	u, err := url.Parse(*base)
	if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || net.ParseIP(u.Hostname()) == nil || !net.ParseIP(u.Hostname()).IsLoopback() || flags.NArg() != 0 {
		return errors.New("a API deve ser um endereço HTTP loopback literal")
	}
	if *app == "" || *secret == "" || *waba == "" || *phone == "" || *from == "" || *body == "" {
		return errors.New("informe app, secret, waba, phone, from e text de simulação")
	}
	messageID := "wamid.simulation.inbound." + uuid.NewString()
	payload, err := json.Marshal(map[string]any{"object": "whatsapp_business_account", "entry": []any{map[string]any{"id": *waba, "changes": []any{map[string]any{"field": "messages", "value": map[string]any{"messaging_product": "whatsapp", "metadata": map[string]string{"phone_number_id": *phone, "display_phone_number": *phone}, "messages": []any{map[string]any{"from": *from, "id": messageID, "timestamp": fmt.Sprint(time.Now().Unix()), "type": "text", "text": map[string]string{"body": *body}}}}}}}}})
	if err != nil {
		return err
	}
	u.Path = "/api/webhooks/meta"
	q := u.Query()
	q.Set("app_id", *app)
	u.RawQuery = q.Encode()
	req, err := http.NewRequest(http.MethodPost, u.String(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	mac := hmac.New(sha256.New, []byte(*secret))
	_, _ = mac.Write(payload)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: &http.Transport{Proxy: nil}}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("webhook de simulação retornou HTTP %d", resp.StatusCode)
	}
	log.Printf("SIMULAÇÃO: webhook aceito, mensagem %s", messageID)
	return nil
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatal(err)
	}
}
