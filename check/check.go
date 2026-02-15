package check

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

var apiURL string

const (
	timeoutSec = 30
	gapMinutes = 30
	startHour  = 18
	startMin   = 0
)

func SetAPIURL(url string) {
	apiURL = url
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

func CheckVehicle(registration string) (*APIResponse, error) {
	payload, err := json.Marshal(map[string]string{"vehicle": registration})
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	client := &http.Client{Timeout: time.Duration(timeoutSec) * time.Second}

	resp, err := client.Post(apiURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("post request: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var result APIResponse
		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, fmt.Errorf("decode response: %w", err)
		}
		return &result, nil
	case http.StatusTooManyRequests:
		return nil, fmt.Errorf("rate limited (429): too many requests, try again later")
	default:
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}
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

func StartScheduler(vehicles []string, notify NotifyFunc) {
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

		time.Sleep(waitDuration)

		checkAllVehicles(vehicles, notify)
	}
}

func checkAllVehicles(vehicles []string, notify NotifyFunc) {
	slog.Info("starting daily vehicle check", "count", len(vehicles))

	for i, reg := range vehicles {
		if i > 0 {
			slog.Info("waiting before next vehicle", "gap", fmt.Sprintf("%d minutes", gapMinutes), "next", reg)
			time.Sleep(time.Duration(gapMinutes) * time.Minute)
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
