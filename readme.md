# TeleCloud

<div align="center">

**[📢 Support Group](https://t.me/+p-d0qfGRbX4wNzJl)**
*Join the community to discuss features, get help, and stay updated*

</div>

**TeleCloud** is a self-hosted cloud storage and streaming platform that turns Telegram’s high-capacity infrastructure into your personal, virtually unlimited cloud drive. Completely engineered in Go for blazing-fast throughput, minimal CPU footprint, and ultra-low memory consumption.

> [!TIP]
> **Mobile Client Version:** A dedicated mobile client is available. Check out the [Mobile Client Guide](./docs/MobileClient.md).

> [!IMPORTANT]
> **Configuration in Version 3.7.0+**
> Starting with v3.7.0, **App ID & API Hash** and **Bot Tokens (Bot Pool)** are managed entirely through the **application's Settings UI**:
> - 🔑 **App ID & API Hash**: During initial setup, simply leave the defaults as provided and click **Continue** — the app includes built-in developer credentials. Only change this if you are an advanced user or developer using custom Telegram API credentials.
> - 🤖 **Bot Tokens (Bot Pool)**: Add, modify, or remove bots dynamically in **Admin Panel → Settings → Bot Pool** without needing to restart the server.

---

## 📸 Preview

### 🖥️ Desktop Interface
| | |
| :---: | :---: |
| <img src="preview/preview.jpg" width="100%"> | <img src="preview/preview-2.jpg" width="100%"> |
| <img src="preview/preview-3.jpg" width="100%"> | <img src="preview/preview-4.jpg" width="100%"> |

### 📱 Mobile Interface
| | | | | |
| :---: | :---: | :---: | :---: | :---: |
| <img src="preview/preview-5.jpg" width="100%"> | <img src="preview/preview-6.jpg" width="100%"> | <img src="preview/preview-7.jpg" width="100%"> | <img src="preview/preview-8.jpg" width="100%"> | <img src="preview/preview-9.jpg" width="100%"> |

---

## ✨ Features

* 📁 **Virtually Unlimited Storage**: Store files directly on Telegram with **no file size limits** (automatically chunks large files from 500MB to 4GB with smart memory pooling).
* 📑 **Modern In-Browser PDF Reader**: Rich PDF viewing experience featuring **default vertical continuous scrolling**, lazy-rendered pages with true aspect-ratio placeholders, outline/TOC navigation, fit-width/fit-height zoom, pinch-to-zoom gestures, smooth auto-scrolling, and direct printing.
* 📚 **EPUB & Comic (CBZ/CBR) Readers**: Built-in readers for EPUB books (chapters, custom themes, typography settings) and comics (Webtoon continuous scroll, single/double page view, progress preservation).
* 🎬 **Media Streaming & Subtitles**: Stream high-definition video and audio files in real time. Automatically scans and loads matching subtitle tracks (`.srt`, `.vtt`, `.ass`) or lets you upload custom subtitles. Includes convenient keyboard shortcuts (`Space` to play/pause, `Left/Right` to seek 5s, `Up/Down` for volume, `N`/`P` for next/previous track).
* 🖼️ **Layered Thumbnails & Fast Previews**: Native Telegram thumbnail prefetching with in-flight deduplication and modern FFmpeg generation, layered seamlessly over file type icons with smooth fade-in transitions.
* 🔗 **Flexible File & Folder Sharing**: Generate secure public or private sharing links with optional password protection and expiration controls. Supports direct download links for single files and entire folders.
* ⚡ **On-The-Fly Folder Download**: High-performance, diskless server-side ZIP streaming allows downloading entire directories on demand without consuming server disk space.
* 🗂️ **Intuitive Management**: Modern web dashboard with both **Grid** and **List** view modes, instant file search, and sorting.
* 📂 **WebDAV Drive Mounting**: Mount TeleCloud directly as a network drive on Windows, macOS, Linux, and mobile file managers.
* 🪣 **S3 API Compatibility**: Built-in S3-compatible API (SigV4/SigV2 authentication, HTTP Range requests) to integrate with third-party software such as Rclone, Cyberduck, and Infuse.
* 🔌 **RESTful Upload API**: Programmatically upload files via HTTP API for automated scripts, CLI tools, and CI/CD pipelines.
* 📥 **URL, Social Media & Telegram (`t.me`) Downloader**: Download directly from URLs and video platforms (YouTube, TikTok, Facebook, etc.) via **yt-dlp**; download restricted media and files directly from Telegram links (`t.me` or `tg://`) using the integrated Userbot.
* 🧲 **Integrated Torrent Downloader**: Download torrents and magnet links directly into your cloud storage using **aria2c**.
* ⚡ **Asynchronous Background Tasks**: Non-blocking download and processing engine with real-time WebSocket progress notifications.
* 🤖 **Bot Pool & Personal File Receipt**: Scale throughput across multiple secondary Telegram bots to bypass rate limits. Users can also connect their personal Telegram account so files sent directly to any pool bot are automatically saved to their personal workspace.
* 👥 **Multi-User System**: Manage independent sub-accounts with isolated storage directories, permission controls, and quota limits.
* 🔐 **Passkey & WebAuthn Security**: Passwordless biometric authentication using Fingerprint, Face ID, or hardware security keys.
* 🗄️ **Multi-Database Support**: Out-of-the-box support for **SQLite**, **MySQL**, and **PostgreSQL** for enterprise workloads.
* 🗑️ **Trash Bin & Recovery**: Safely recover deleted files and protect your data against accidental removal.
* 🛡️ **Automated Daily Backups**: Scheduled automatic backups of databases and metadata directly to Telegram.
* 🌐 **Internationalization**: Fully localized in English by default, with built-in multi-language translation support.

---

## 🚀 Quick Start

The fastest way to install and run TeleCloud is using the automated setup script:

### Linux / Termux / macOS / Raspberry Pi
```bash
curl -fsSL https://raw.githubusercontent.com/dabeecao/telecloud-go/main/auto-setup-en.sh -o auto-setup-en.sh && bash auto-setup-en.sh
```

### Windows
Download [**`auto-install-en.bat`**](https://raw.githubusercontent.com/dabeecao/telecloud-go/main/auto-install-en.bat) and run as **Administrator**.

---

## 📖 Documentation & Guides

For in-depth guides, configuration parameters, and custom setups:

* [🛠️ **Installation Guide**](./docs/Installation.md) (Binary, Windows, Linux, systemd service)
* [⚙️ **Configuration Guide**](./docs/Configuration.md) (Environment variables, Nginx reverse proxy, SSL)
* [🐳 **Docker Deployment Guide**](./docs/Docker.md) (Docker Compose, container tuning)
* [📱 **Mobile Client Guide**](./docs/MobileClient.md) (App download, pairing, and features)
* [🔌 **API Documentation**](./docs/API.md) (REST endpoints, Upload API, authentication)
* [🔐 **Security Policy**](./docs/Security.md) (Encryption standards, hardening recommendations)
* [🛠️ **Development & Build Guide**](./docs/Development.md) (Building from source, contributing)

---

## 🔐 Security Architecture

TeleCloud follows strict security best practices:
- AES-256-GCM authenticated encryption for sensitive credentials and tokens
- WebAuthn / FIDO2 passkey support for phishing-resistant logins
- WebDAV rate-limiting, CSRF token verification, and hardened Content Security Policies (CSP)
- SSRF and DNS Rebinding protection on remote URL download handlers
- Ephemeral, diskless streaming architecture for reduced attack surface

For full details and deployment recommendations, read the [**Security Policy & Hardening Guide**](./docs/Security.md).

---

## ⚠️ Terms of Use & Disclaimer

**TeleCloud** is developed for legitimate personal file storage, media management, and backup purposes. The project developers are not responsible for any content uploaded by users or any violations of Telegram's Terms of Service. Users bear full and sole responsibility for how they use this software.

This project is distributed on an **“as-is”** basis, without warranties or guarantees of any kind.

---

## 🙏 Credits & Open-Source Ecosystem

TeleCloud is powered by these open-source projects and libraries:
* [gotd/td](https://github.com/gotd/td): High-performance native Go Telegram client (MTProto API)
* [Gin](https://github.com/gin-gonic/gin): HTTP web framework for Go
* [AlpineJS](https://github.com/alpinejs/alpine): Reactive, lightweight client-side framework
* [TailwindCSS](https://github.com/tailwindlabs/tailwindcss): Utility-first styling framework
* [Artplayer.js](https://github.com/zhw2590582/ArtPlayer): HTML5 video player with chapter & subtitle support
* [PDF.js](https://github.com/mozilla/pdf.js): In-browser PDF rendering engine
* [plyr](https://github.com/sampotts/plyr): Accessible media player for audio files
* [Prism.js](https://github.com/PrismJS/prism): Extensible code syntax highlighter
* [FontAwesome](https://fontawesome.com): Modern iconography
* [Bun](https://bun.com): Fast JavaScript runtime & asset build toolkit
* [yt-dlp](https://github.com/yt-dlp/yt-dlp): Media download engine
* [aria2](https://github.com/aria2/aria2): Multi-protocol torrent & chunk downloader
* [Google Fonts (Nunito)](https://fonts.google.com/specimen/Nunito): Typography

Special thanks to all open-source maintainers and community contributors.

<a href="https://github.com/dabeecao/telecloud-go/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=dabeecao/telecloud-go" />
</a>

---

## 📜 License

This project is licensed under the [GNU Affero General Public License v3.0 (AGPL-3.0)](https://www.gnu.org/licenses/agpl-3.0.html).
