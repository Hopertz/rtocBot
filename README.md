# rtocBot

A Telegram bot that checks vehicle road traffic offences from the Tanzania RTOC system.

## Features

- Daily automated checks at 18:00 with 10-minute gaps between vehicles
- On-demand checks via `/check` command
- Pending offences with charges and penalties
- Vehicle inspection history
- Rate limit handling (429)

## Environment Variables

| Variable | Description |
|---|---|
| `TG_BOT_TOKEN` | Telegram Bot API token |
| `VEHICLES` | Comma-separated vehicle registrations e.g. `T945CAP,T267DFF` |
| `MASTER_ID` | Your Telegram chat ID |
| `RTOC_API_URL` | RTOC API endpoint

## Build & Run

```bash
go build -o bot ./cmd/bot

export TG_BOT_TOKEN=your_token
export VEHICLES=T945CAP,T267DFF
export MASTER_ID=7312133
export RTOC_API_URL=https://url.example.com/api/OffenceCheck

./bot
```

## Bot Commands

| Command | Description |
|---|---|
| `/start` | Start the bot |
| `/help` | Show available commands |
| `/check` | Check all listed vehicles |
| `/check T945CAP` | Check a specific vehicle |

## Project Structure

```
├── cmd/bot/bot.go    # Telegram bot entry point
├── check/check.go    # RTOC API client & scheduler
├── go.mod
└── .env              # Environment variables (not committed)
```
