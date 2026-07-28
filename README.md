# Telegram File Stream Bot (Go Version)

A high-speed, lightweight Telegram bot and web server written in Go that generates direct HTTP streaming and download links for files stored on Telegram. It pipes files directly from Telegram's servers on-the-fly, enabling immediate playback and seek support without pre-downloading them to local storage.

---

## Features

- **On-the-Fly Streaming:** Streams files directly from Telegram servers to the client in real-time.
- **HTTP Range Request Support:** Full support for `Accept-Ranges` allows seeking (fast-forwarding/rewinding) in video and audio players (VLC, MX Player, web browsers).
- **Secure Links:** Links are cryptographically secured with custom-length hashes to prevent unauthorized access.
- **Load Balancing (Workers):** Multi-token support allows the server to distribute download chunk requests among multiple bot/user accounts, boosting speed and avoiding rate limits.
- **Log Channel Integration:** Automatically logs and persists media messages to a private channel to ensure files remain accessible.

---

## Tech Stack

This project is built using the following modern Go libraries and technologies:

- **Language:** [Go](https://go.dev/) (1.25+) for native performance and concurrency.
- **Telegram Client (MTProto):** [gotd/td](https://github.com/gotd/td) and [gotgproto](https://github.com/celestix/gotgproto) for secure, high-performance interactions with the Telegram API.
- **Web Server:** [Gin Web Framework](https://github.com/gin-gonic/gin) for robust routing and streaming endpoints.
- **CLI Framework:** [Cobra](https://github.com/spf13/cobra) for structured, easy-to-use command-line interface commands.
- **Database:** SQLite (via [glebarez/sqlite](https://github.com/glebarez/sqlite)) to handle session storage locally.

---

## Local Setup & Installation

### Prerequisites
- Go compiler (version 1.21+ recommended) installed on your system.
- Telegram API credentials (`API_ID`, `API_HASH`) from [my.telegram.org](https://my.telegram.org).
- Telegram Bot Token (`BOT_TOKEN`) from [@BotFather](https://t.me/BotFather).
- A private Telegram channel where your bot is an Administrator (`LOG_CHANNEL`).

### Configuration
1. Copy the sample environment file to create `fsb.env`:
   ```bash
   cp fsb.sample.env fsb.env
   ```
2. Open `fsb.env` and fill in the required variables:
   ```env
   API_ID=your_api_id
   API_HASH=your_api_hash
   BOT_TOKEN=your_bot_token
   LOG_CHANNEL=-100xxxxxxxxxx
   ```

### Running the App
1. Download Go package dependencies:
   ```bash
   go mod download
   ```
2. Run the bot server directly:
   ```bash
   go run ./cmd/fsb run
   ```
3. (Optional) Compile and run the binary:
   ```bash
   go build -o fsb ./cmd/fsb
   ./fsb run
   ```

---

## Copyright

Copyright (C) 2026 XeonModz under [GNU Affero General Public License](https://www.gnu.org/licenses/agpl-3.0.en.html).

TG-FileStreamBot is Free Software: You can use, study, share, and improve it at your will. Specifically, you can redistribute and/or modify it under the terms of the GNU Affero General Public License as published by the Free Software Foundation, either version 3 of the License, or (at your option) any later version. All forks of this repository must remain open-source under the same license.
