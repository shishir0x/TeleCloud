# TeleCloud

<div align="center">

🇺🇸 English | [🇻🇳 Tiếng Việt](./readme_vi.md)

**[📢 Support Group](https://t.me/+p-d0qfGRbX4wNzJl)**
*Join the group to discuss and get support*

</div>

**TeleCloud** is a project that allows you to use Telegram’s nearly unlimited storage capacity to store and manage files. Completely rewritten in Golang for excellent performance and low memory usage.

> [!TIP]
> **Mobile Client Version:** The project now has a mobile client available, [download & see guide here](./docs/MobileClient.md).

> [!IMPORTANT]
> **Changed since version 3.7.0**
> Starting from v3.7.0, **App ID & API Hash** and **Bot Tokens (Bot Pool)** are no longer configured in the `.env` file — everything is managed directly inside the **application's Settings UI**:
> - 🔑 **App ID & API Hash**: During the initial setup, **just leave it as default and click Continue** — the app already has the developer credentials built in. Only change this if you are an **advanced user / developer** who wants to use their own API credentials.
> - 🤖 **Bot Tokens (Bot Pool)**: Add, edit, or remove bots directly in **Admin Panel → Settings → Bot Pool** — no server restart required.

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

* 📁 **Unlimited Storage**: Store files directly on Telegram with **no size limits** (Automatically splits large files into chunks from 500MB to 4GB).
* 🎬 **Media & Subtitles Streaming**: Stream videos and music directly in the dashboard and shared links. Seamlessly auto-scans and loads subtitles (.srt, .vtt, .ass) with matching names, or manually uploads external subtitles from your computer. Supports convenient shortcut keys (`Space` to play/pause, `Left/Right Arrows` to seek 5s, `Up/Down Arrows` for volume, `N` for next track, and `P` for previous track).
* 📚 **EPUB, Comic & PDF Readers**: Integrates custom, high-fidelity online readers for **EPUB** e-books, **CBZ** comic books (Webtoon scroll mode, lazy-loading, automatic progress preservation), and **PDF** documents directly within the browser without downloading.
* 🔗 **Flexible Sharing**: Supports normal or direct download links (Direct Link) for both files and **folders**.
* ⚡ **On-The-Fly Folder Download**: Supports high-performance, diskless server-side ZIP streaming to download entire directories instantly without using any server disk storage (exclusively restricted to logged-in users for optimal efficiency).
* 🗂️ **Intuitive Management**: File Browser with **Grid** and **List** view modes.
* ⬆️ **High Performance**: Multi-threaded and chunked uploads for maximum speed and stability.
* 📂 **WebDAV Support**: Mount TeleCloud as a network drive on Windows, macOS, and Linux.
* 🪣 **S3 API Compatibility**: Provides an S3-compatible API (via gofakes3) to integrate with third-party clients (Rclone, Cyberduck, Infuse, etc.) supporting secure SigV4/SigV2 signature verification and Range requests for smooth video streaming.
* 🔌 **Upload API**: Remote file uploads via HTTP API for script or CI/CD integration.
* 📥 **URL, Media & Telegram (`t.me`) Downloader**: Download files from URLs and Media (YouTube, TikTok, Facebook...) using **yt-dlp**; especially supports resolving and **downloading restricted files directly** from Telegram links (`t.me` or `tg://`) via the integrated Userbot.
* ⚡ **Background Tasks**: Background URL downloads with real-time progress notifications.
* 🧲 **Torrent Support**: Download Torrents and Magnet links directly to Telegram via **aria2c**.
* 👥 **Multi-user**: Support for child accounts with isolated storage spaces (Virtual Path).
* 🤖 **Multi-Bot (Bot Pool) & Personal File Receipt**: Distribute workloads across multiple secondary bots to maximize speed and reliability. Also allows each user (including child accounts) to link their personal Telegram account to instantly upload and organize files sent directly to any bot in the Bot Pool into their own storage workspace.
* 🔐 **Passkey Security**: Biometric login (Fingerprint, FaceID) or security keys (WebAuthn).
* 🗄️ **Multi-Database**: Supports **SQLite**, **MySQL**, and **PostgreSQL** for enterprise-scale needs.
* 🗑️ **Trash Bin**: Recover deleted files and protect data from accidental removal.
* 🔒 **Protected Shares**: Set password protection for shared files and folders.
* 🛡️ **Auto Backup**: Daily automated backups of database and thumbnails to Telegram.
* 🌐 **Multi-language**: Supports English (default), Vietnamese, Chinese, Japanese, Russian, and more.

---

## 🚀 Quick Start

Use the automated script — the easiest way to get started:

### Linux / Termux / macOS / Raspberry Pi
```bash
curl -fsSL https://raw.githubusercontent.com/dabeecao/telecloud-go/main/auto-setup-en.sh -o auto-setup-en.sh && bash auto-setup-en.sh
```

### Windows
Download [**`auto-install-en.bat`**](https://raw.githubusercontent.com/dabeecao/telecloud-go/main/auto-install-en.bat) and run as **Administrator**.

---

## 📖 Detailed Documentation (Wiki)

For configuration details and alternative installation methods, please refer to the documentation:

*   [🛠️ **Installation Guide**](./docs/Installation.md) (Binary, Windows, Linux...)
*   [⚙️ **Configuration Guide**](./docs/Configuration.md) (.env, Nginx Proxy...)
*   [🐳 **Docker Deployment**](./docs/Docker.md) (Docker Run, Compose)
*   [📱 **Mobile Client Guide**](./docs/MobileClient.md) (Installation & Connection)
*   [🔌 **API Documentation**](./docs/API.md) (Upload API Guide)
*   [🔐 **Security Policy**](./docs/Security.md) (Encryption, Hardening & Warnings)
*   [🛠️ **Development & Localization**](./docs/Development.md) (Build from source, Contribute)

---

## 🔐 Security

TeleCloud is designed with optimized security standards (including AES-256-GCM encryption for sensitive data, systemd hardening, WebDAV rate limiting, SSRF/DNS Rebinding mitigation, CSP, etc.).

For more detailed information regarding security architecture, operational recommendations, and known limitations, please refer to:
👉 [**Security Policy & Hardening Guide**](./docs/Security.md)

---

## ⚠️ Terms of Use & Disclaimer
 
**TeleCloud** is developed for storing and managing legitimate personal files. We are not responsible for any content uploaded by users or violations of Telegram’s terms of service. Users are **fully responsible** for their own actions.

The project is provided **“as-is”**, without any guarantees of stability or security.

---

## 🙏 Credits

This project uses amazing libraries:
* [gotd/td](https://github.com/gotd/td): Telegram client (MTProto API)
* [Gin](https://github.com/gin-gonic/gin): High-performance HTTP web framework
* [AlpineJS](https://github.com/alpinejs/alpine): Minimal JS framework
* [TailwindCSS](https://github.com/tailwindlabs/tailwindcss): Utility-first CSS framework
* [plyr](https://github.com/sampotts/plyr): HTML5 media player
* [Artplayer.js](https://github.com/zhw2590582/ArtPlayer): Modern and full-featured HTML5 video player.
* [PDF.js](https://github.com/mozilla/pdf.js): HTML5 PDF reader and viewer.
* [Prism.js](https://github.com/PrismJS/prism): Lightweight, extensible syntax highlighter.
* [FontAwesome](https://fontawesome.com): The world's most popular icon set.
* [Bun](https://bun.com): Fast JavaScript runtime & toolkit (build toolchain).
* [yt-dlp](https://github.com/yt-dlp/yt-dlp): Audio/video downloader.
* [aria2](https://github.com/aria2/aria2): Multi-protocol download utility.
* [Google Fonts (Nunito)](https://fonts.google.com/specimen/Nunito): Modern sans-serif typeface.

Thanks to all development teams and **contributors** for providing great tools and efforts for the community.

<a href="https://github.com/dabeecao/telecloud-go/graphs/contributors">
  <img src="https://contrib.rocks/image?repo=dabeecao/telecloud-go" />
</a>

**A portion of the project's source code and this readme was referenced and modified by Gemini AI.**

---

## 📜 License

This project is licensed under the [GNU Affero General Public License v3.0 (AGPL-3.0)](https://www.gnu.org/licenses/agpl-3.0.html).
