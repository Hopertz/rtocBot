package check

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

var apiURL string

const (
	timeoutSec  = 30
	gapMinutes  = 30
	jitterMin   = 5
	startHour   = 18
	startMin    = 0
	maxRetries  = 3
	baseBackoff = 2 * time.Minute
)

func SetAPIURL(url string) {
	apiURL = url
}

const encryptionKey = "irtismutDkjQBbZKEUn8hw7WqKdxld01E6HIY"

type encryptedResponse struct {
	Payload string `json:"payload"`
}

type PendingTransaction struct {
	Reference  string  `json:"reference"`
	IssuedDate string  `json:"issued_date"`
	Operator   string  `json:"operator"`
	Vehicle    string  `json:"vehicle"`
	Licence    string  `json:"licence"`
	Location   string  `json:"location"`
	Offence    string  `json:"offence"`
	Charge     string  `json:"charge"`
	Penalty    string  `json:"penalty"`
	Status     string  `json:"status"`
	Receipt    *string `json:"receipt"`
	PayDate    *string `json:"paydate"`
	PenDate    *string `json:"pendate"`
}

type InspectionData struct {
	ID               int    `json:"id"`
	VirNo            string `json:"vir_no"`
	FinalResult      string `json:"finalresult"`
	Inspector        string `json:"inspector"`
	Region           string `json:"region"`
	District         string `json:"district"`
	ProhibitionOnUse string `json:"prohibition_on_use"`
	Weight           string `json:"weight"`
	Licence          string `json:"licence"`
	DriverName       string `json:"driver_name"`
	VehiclePassedFor string `json:"vehicle_passed_for"`
	InspectionDate   string `json:"inspection_date"`
	ValidUntil       string `json:"valid_untill"`
	NoPlate          string `json:"noplate"`
	ReasonEN         string `json:"reason_en"`
	Remarks          string `json:"remarks"`
}

type APIResponse struct {
	Status              string               `json:"status"`
	TotalPendingAmount  *string              `json:"totalPendingAmount"`
	PendingTransactions []PendingTransaction `json:"pending_transactions"`
	InspectionData      []InspectionData     `json:"inspection_data"`
}

func b64Decode(s string) ([]byte, error) {
	// Strip any whitespace (node-forge's decode64 is lenient)
	s = strings.Map(func(r rune) rune {
		if r == ' ' || r == '\n' || r == '\r' || r == '\t' {
			return -1
		}
		return r
	}, s)

	// Add padding if missing
	if m := len(s) % 4; m != 0 {
		s += strings.Repeat("=", 4-m)
	}

	return base64.StdEncoding.DecodeString(s)
}

func isBase64(data []byte) bool {
	s := strings.TrimSpace(string(data))
	if len(s) == 0 || len(s)%4 != 0 {
		return false
	}
	for _, c := range s {
		if !((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '+' || c == '/' || c == '=') {
			return false
		}
	}
	return true
}

func decryptPayload(payload string) ([]byte, error) {
	keyBytes := []byte(encryptionKey[:32])

	hash := sha256.Sum256([]byte(encryptionKey))
	ivHex := hex.EncodeToString(hash[:])[:16]
	ivBytes := []byte(ivHex)

	// First base64 decode
	decoded, err := b64Decode(payload)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}

	// Check for double base64 encoding (matches website JS logic)
	if isBase64(decoded) {
		inner, err := b64Decode(string(decoded))
		if err == nil && len(inner) > 0 {
			slog.Info("double base64 detected, unwrapped one layer")
			decoded = inner
		}
	}

	if len(decoded)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("ciphertext length %d is not a multiple of block size %d", len(decoded), aes.BlockSize)
	}

	block, err := aes.NewCipher(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("new cipher: %w", err)
	}

	mode := cipher.NewCBCDecrypter(block, ivBytes)
	plaintext := make([]byte, len(decoded))
	mode.CryptBlocks(plaintext, decoded)

	// PKCS7 unpad
	if len(plaintext) == 0 {
		return nil, fmt.Errorf("empty plaintext after decryption")
	}
	padLen := int(plaintext[len(plaintext)-1])
	if padLen > aes.BlockSize || padLen == 0 {
		return nil, fmt.Errorf("invalid PKCS7 padding: %d", padLen)
	}
	for i := len(plaintext) - padLen; i < len(plaintext); i++ {
		if plaintext[i] != byte(padLen) {
			return nil, fmt.Errorf("invalid PKCS7 padding at byte %d", i)
		}
	}
	plaintext = plaintext[:len(plaintext)-padLen]

	return plaintext, nil
}

func doRequest(payload []byte) (*http.Response, error) {
	req, err := http.NewRequest(http.MethodPost, apiURL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Origin", "https://tms.tpf.go.tz")
	req.Header.Set("Referer", "https://tms.tpf.go.tz/")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	client := &http.Client{Timeout: time.Duration(timeoutSec) * time.Second}
	return client.Do(req)
}

func CheckVehicle(registration string) (*APIResponse, error) {
	payload, err := json.Marshal(map[string]string{"vehicle": registration})
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	var lastErr error
	for attempt := range maxRetries {
		if attempt > 0 {
			backoff := baseBackoff * time.Duration(1<<(attempt-1))
			jitter := time.Duration(rand.Int63n(int64(30 * time.Second)))
			wait := backoff + jitter
			slog.Info("retrying after rate limit", "attempt", attempt+1, "wait", wait.Round(time.Second))
			time.Sleep(wait)
		}

		resp, err := doRequest(payload)
		if err != nil {
			return nil, fmt.Errorf("post request: %w", err)
		}

		switch resp.StatusCode {
		case http.StatusOK:
			var encrypted encryptedResponse
			if err := json.NewDecoder(resp.Body).Decode(&encrypted); err != nil {
				resp.Body.Close()
				return nil, fmt.Errorf("decode response: %w", err)
			}
			resp.Body.Close()

			if encrypted.Payload == "" {
				return nil, fmt.Errorf("empty payload in response")
			}

			decrypted, err := decryptPayload(encrypted.Payload)
			if err != nil {
				return nil, fmt.Errorf("decrypt payload: %w", err)
			}

			var result APIResponse
			if err := json.Unmarshal(decrypted, &result); err != nil {
				return nil, fmt.Errorf("decode decrypted response: %w", err)
			}
			return &result, nil

		case http.StatusTooManyRequests:
			resp.Body.Close()
			lastErr = fmt.Errorf("rate limited (429): attempt %d/%d", attempt+1, maxRetries)
			slog.Warn("rate limited by API", "attempt", attempt+1, "max", maxRetries)
			continue

		default:
			resp.Body.Close()
			return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
		}
	}

	return nil, fmt.Errorf("gave up after %d retries: %w", maxRetries, lastErr)
}

func FormatResult(registration string, data *APIResponse) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "🚗 *RTOC Report for %s*\n", registration)
	fmt.Fprintf(&sb, "━━━━━━━━━━━━━━━━━━━━━\n")

	if data.Status != "success" {
		fmt.Fprintf(&sb, "❌ No results found.\n")
		return sb.String()
	}

	if len(data.PendingTransactions) > 0 {
		total := "N/A"
		if data.TotalPendingAmount != nil {
			total = *data.TotalPendingAmount
		}
		fmt.Fprintf(&sb, "⚠️ *Pending Offences: %d* (Total: %s TZS)\n\n", len(data.PendingTransactions), total)

		for i, txn := range data.PendingTransactions {
			fmt.Fprintf(&sb, "*%d.* %s\n", i+1, txn.Offence)
			fmt.Fprintf(&sb, "   📍 %s\n", txn.Location)
			fmt.Fprintf(&sb, "   💰 Charge: %s | Penalty: %s\n", txn.Charge, txn.Penalty)
			fmt.Fprintf(&sb, "   🔖 Ref: %s\n", txn.Reference)
			fmt.Fprintf(&sb, "   📅 Issued: %s\n", txn.IssuedDate)
			fmt.Fprintf(&sb, "   📋 Status: %s\n\n", txn.Status)
		}
	} else {
		fmt.Fprintf(&sb, "✅ No pending offences.\n\n")
	}

	if len(data.InspectionData) > 0 {
		fmt.Fprintf(&sb, "🔍 *Inspection Records: %d*\n\n", len(data.InspectionData))

		for i, ins := range data.InspectionData {
			date := ins.InspectionDate
			if len(date) > 10 {
				date = date[:10]
			}
			validUntil := ins.ValidUntil
			if len(validUntil) > 10 {
				validUntil = validUntil[:10]
			}
			fmt.Fprintf(&sb, "*%d.* %s — *%s*\n", i+1, ins.ReasonEN, ins.FinalResult)
			fmt.Fprintf(&sb, "   📅 %s → %s\n", date, validUntil)
			fmt.Fprintf(&sb, "   📍 %s, %s\n", ins.Region, ins.District)
			if ins.Remarks != "" {
				fmt.Fprintf(&sb, "   📝 %s\n", ins.Remarks)
			}
			fmt.Fprintf(&sb, "\n")
		}
	}

	return sb.String()
}

func ParseVehicles(vehicles string) []string {
	parts := strings.Split(vehicles, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		v := strings.TrimSpace(p)
		if v != "" {
			result = append(result, strings.ToUpper(v))
		}
	}
	return result
}

type NotifyFunc func(text string) error

func StartScheduler(ctx context.Context, vehicles []string, notify NotifyFunc) {
	eat := time.FixedZone("EAT", 3*60*60)
	slog.Info("scheduler started", "vehicles", vehicles, "start_time", fmt.Sprintf("%02d:%02d EAT", startHour, startMin))

	for {
		now := time.Now().In(eat)
		next := time.Date(now.Year(), now.Month(), now.Day(), startHour, startMin, 0, 0, eat)

		if now.After(next) {
			next = next.Add(24 * time.Hour)
		}

		waitDuration := time.Until(next)
		slog.Info("next scheduled run", "at", next.Format("2006-01-02 15:04:05"), "in", waitDuration.Round(time.Second))

		select {
		case <-ctx.Done():
			slog.Info("scheduler stopped")
			return
		case <-time.After(waitDuration):
		}

		checkAllVehicles(ctx, vehicles, notify)
	}
}

func checkAllVehicles(ctx context.Context, vehicles []string, notify NotifyFunc) {
	slog.Info("starting daily vehicle check", "count", len(vehicles))

	for i, reg := range vehicles {
		if i > 0 {
			jitterDur := time.Duration(rand.Int63n(int64(jitterMin))) * time.Minute
			gap := time.Duration(gapMinutes)*time.Minute + jitterDur
			slog.Info("waiting before next vehicle", "gap", gap.Round(time.Second), "next", reg)
			select {
			case <-ctx.Done():
				slog.Info("vehicle check stopped")
				return
			case <-time.After(gap):
			}
		}

		select {
		case <-ctx.Done():
			slog.Info("vehicle check stopped")
			return
		default:
		}

		slog.Info("checking vehicle", "registration", reg)

		data, err := CheckVehicle(reg)
		if err != nil {
			slog.Error("failed to check vehicle", "registration", reg, "err", err)
			errMsg := fmt.Sprintf("❌ Failed to check *%s*: %s", reg, err.Error())
			if notifyErr := notify(errMsg); notifyErr != nil {
				slog.Error("failed to send error notification", "err", notifyErr)
			}
			continue
		}

		msg := FormatResult(reg, data)
		if err := notify(msg); err != nil {
			slog.Error("failed to send notification", "registration", reg, "err", err)
		}
	}

	slog.Info("daily vehicle check completed")
}
