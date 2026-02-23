package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Hopertz/rtocBot/check"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	start_txt   = "✅ Background checks started. Use /check to check for vehicle road traffic offences or wait for vehicle road traffic offences notifications for listed vehicles. Type /stop to stop receiving notifications."
	unknown_cmd = "I don't know that command"
)

func init() {

	var programLevel = new(slog.LevelVar)
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: programLevel})
	slog.SetDefault(slog.New(h))

}

func main() {

	var bot_token string
	var vehicles string
	var masterIDStr string
	var apiURL string

	flag.StringVar(&bot_token, "bot-token", os.Getenv("TG_BOT_TOKEN"), "Bot Token")
	flag.StringVar(&vehicles, "vehicles", os.Getenv("VEHICLES"), "Vehicles")
	flag.StringVar(&masterIDStr, "master-id", os.Getenv("MASTER_ID"), "Master Chat ID")
	flag.StringVar(&apiURL, "api-url", os.Getenv("RTOC_API_URL"), "RTOC API URL")
	flag.Parse()

	if bot_token == "" {
		slog.Error("Bot token not provided")
		return
	}

	if vehicles == "" {
		slog.Error("Vehicles not provided")
		return
	}

	if masterIDStr == "" {
		slog.Error("Master ID not provided")
		return
	}

	masterID, err := strconv.ParseInt(masterIDStr, 10, 64)
	if err != nil {
		slog.Error("Invalid master ID", "err", err)
		return
	}

	if apiURL == "" {
		slog.Error("RTOC API URL not provided")
		return
	}

	check.SetAPIURL(apiURL)

	bot, err := tgbotapi.NewBotAPI(bot_token)
	if err != nil {
		slog.Error("failed to create bot api instance", "err", err)
		return
	}

	vehicleList := check.ParseVehicles(vehicles)
	slog.Info("loaded vehicles", "count", len(vehicleList), "vehicles", vehicleList)

	notify := func(text string) error {
		msg := tgbotapi.NewMessage(masterID, text)
		msg.ParseMode = "Markdown"
		_, err := bot.Send(msg)
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	var checkCancel context.CancelFunc

	go check.StartScheduler(ctx, vehicleList, notify)

	u := tgbotapi.NewUpdate(0)

	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message == nil {
			continue
		}

		if update.Message.Chat.ID != masterID {
			continue
		}

		if !update.Message.IsCommand() {
			continue
		}

		msg := tgbotapi.NewMessage(masterID, "")

		switch update.Message.Command() {
		case "start":
			cancel()
			ctx, cancel = context.WithCancel(context.Background())
			go check.StartScheduler(ctx, vehicleList, notify)
			msg.Text = start_txt

		case "help":
			msg.Text = `Commands:
/start  start background checks
/stop   stop background checks
/check  check all listed vehicles
/check <REG>  check a specific vehicle`

		case "check":
			args := strings.TrimSpace(update.Message.CommandArguments())

			var regs []string
			if args != "" {
				regs = []string{strings.ToUpper(args)}
			} else {
				regs = vehicleList
			}

			msg.Text = fmt.Sprintf("🔎 Checking %d vehicle(s)...", len(regs))
			msg.ParseMode = "Markdown"
			if _, err := bot.Send(msg); err != nil {
				slog.Error("failed to send msg", "err", err)
			}

			if checkCancel != nil {
				checkCancel()
			}
			var checkCtx context.Context
			checkCtx, checkCancel = context.WithCancel(ctx)

			go func(c context.Context, registrations []string) {
				for i, reg := range registrations {
					if i > 0 {
						select {
						case <-c.Done():
							return
						case <-time.After(10 * time.Minute):
						}
					}
					select {
					case <-c.Done():
						return
					default:
					}
					data, err := check.CheckVehicle(reg)
					reply := tgbotapi.NewMessage(masterID, "")
					reply.ParseMode = "Markdown"

					if err != nil {
						reply.Text = fmt.Sprintf("❌ Failed to check *%s*: %s", reg, err.Error())
					} else {
						reply.Text = check.FormatResult(reg, data)
					}

					if _, err := bot.Send(reply); err != nil {
						slog.Error("failed to send check result", "err", err, "registration", reg)
					}
				}
			}(checkCtx, regs)
			continue

		case "stop":
			cancel()
			if checkCancel != nil {
				checkCancel()
			}
			msg.Text = "🛑 Background checks stopped. Use /start to resume."

		default:
			msg.Text = unknown_cmd
		}

		if _, err := bot.Send(msg); err != nil {
			slog.Error("failed to send msg", "err", err, "msg", msg)
		}

	}
}
