# 🛠️ Installation Guide

This guide covers the different ways to install TeleCloud on various platforms.

---

## 🇺🇸 English

### 1. Automatic Installation (Recommended)

This is the easiest way to install, configure, and manage TeleCloud. The script installs dependencies (FFmpeg, Tmux, Cloudflared...), configures the service, and provides a management menu.

#### On Windows
1. Download [**`auto-install-en.bat`**](https://raw.githubusercontent.com/dabeecao/telecloud-go/main/auto-install-en.bat) into the installation folder.
2. Right-click and select **Run as Administrator**.
3. Use the Menu to:
    * Automatically install FFmpeg & Cloudflared.
    * Download the latest TeleCloud release.
    * Configure Cloudflare Tunnel (custom domain).
    * Start/Stop the background service and view logs.

#### On Linux / Termux / macOS / Raspberry Pi
Supports Ubuntu, Debian, CentOS, Arch, macOS (Homebrew), Termux, and ARM (Raspberry Pi).

```bash
# Using curl (Recommended)
curl -fsSL https://raw.githubusercontent.com/dabeecao/telecloud-go/main/auto-setup-en.sh -o auto-setup-en.sh && bash auto-setup-en.sh

# Or using wget
wget -qO auto-setup-en.sh https://raw.githubusercontent.com/dabeecao/telecloud-go/main/auto-setup-en.sh && bash auto-setup-en.sh
```

**⚠️ Termux note**: You should install Termux from [GitHub Releases](https://github.com/termux/termux-app/releases) or [F-Droid](https://f-droid.org/packages/com.termux/).

---

### 2. Manual Installation (Using Prebuilt Binary)

#### Step 1: System Requirements
You need **FFmpeg**, **yt-dlp** and **aria2** (optional) for the full feature set:
*   **Ubuntu/Debian**: `sudo apt install ffmpeg python3` and download the yt-dlp binary.
*   **Redhat-based**: `sudo yum install ffmpeg python3` via RPM Fusion.
*   **Alpine Linux**: `apk add ffmpeg python3 yt-dlp aria2`
*   **Windows**: Download FFmpeg, yt-dlp, and aria2 binaries and add them to PATH.

#### Step 2: Download & Startup
1. Get the binary from [**Releases**](https://github.com/dabeecao/telecloud-go/releases).
2. Run the application:
   ```bash
   ./telecloud # Linux/macOS
   telecloud.exe # Windows
   ```
3. Access `http://localhost:8091/setup` to finish configuration with the Web Setup Wizard.
   *   **Note**: Telegram API credentials are pre-configured by default. General users **do not need to provide them**, and the wizard will automatically skip the API setup step. If you are a developer or advanced user wishing to use custom credentials, you can configure them in the **Advanced settings** section at the bottom of the Web Setup login page.

---

## 🇻🇳 Tiếng Việt

### 1. Cài đặt tự động (Khuyên dùng)

Đây là cách đơn giản nhất để cài đặt, cấu hình và quản lý TeleCloud. Script sẽ tự động cài đặt các phụ thuộc (FFmpeg, Tmux, Cloudflared...), cấu hình dịch vụ và cung cấp menu quản lý.

#### Trên Windows
1. Tải tệp [**`auto-install.bat`**](https://raw.githubusercontent.com/dabeecao/telecloud-go/main/auto-install.bat) về thư mục cài đặt.
2. Click chuột phải và chọn **Run as Administrator**.
3. Sử dụng Menu để:
    * Tự động cài đặt FFmpeg & Cloudflared.
    * Tải phiên bản TeleCloud mới nhất.
    * Cấu hình Cloudflare Tunnel (tên miền riêng).
    * Khởi động/Dừng ứng dụng chạy ngầm và xem log.

#### Trên Linux / Termux / macOS / Raspberry Pi
Script hỗ trợ Ubuntu, Debian, CentOS, Arch, macOS (Homebrew), Termux và ARM (Raspberry Pi).

```bash
# Sử dụng curl (Khuyên dùng)
curl -fsSL https://raw.githubusercontent.com/dabeecao/telecloud-go/main/auto-setup.sh -o auto-setup.sh && bash auto-setup.sh

# Hoặc sử dụng wget
wget -qO auto-setup.sh https://raw.githubusercontent.com/dabeecao/telecloud-go/main/auto-setup.sh && bash auto-setup.sh
```

**⚠️ Lưu ý khi dùng Termux**: Bạn nên tải Termux từ [GitHub Releases](https://github.com/termux/termux-app/releases) hoặc [F-Droid](https://f-droid.org/packages/com.termux/).

---

### 2. Cài đặt thủ công (Sử dụng Binary đã biên dịch)

#### Bước 1: Yêu cầu hệ thống
Bạn cần cài đặt **FFmpeg**, **yt-dlp** và **aria2** (tùy chọn) để sử dụng đầy đủ tính năng:
*   **Ubuntu/Debian**: `sudo apt install ffmpeg python3` và tải yt-dlp binary.
*   **Redhat-base**: `sudo yum install ffmpeg python3` thông qua RPM Fusion.
*   **Alpine Linux**: `apk add ffmpeg python3 yt-dlp aria2`
*   **Windows**: Tải bản build sẵn của FFmpeg, yt-dlp, aria2 và thêm vào PATH.

#### Bước 2: Tải về và Khởi động
1. Truy cập mục [**Releases**](https://github.com/dabeecao/telecloud-go/releases) và tải về phiên bản phù hợp.
2. Khởi động ứng dụng:
   ```bash
   ./telecloud # Linux/macOS
   telecloud.exe # Windows
   ```
3. Truy cập `http://localhost:8091/setup` để hoàn tất cấu hình qua giao diện Web Wizard.
   *   **Lưu ý**: Thông tin API Telegram mặc định đã được tích hợp sẵn. Người dùng thông thường **không cần cung cấp** và hệ thống sẽ tự động bỏ qua bước nhập API. Nếu bạn là nhà phát triển hoặc người dùng nâng cao muốn sử dụng API riêng, bạn có thể tùy chỉnh tại phần **Cài đặt nâng cao** ở chân trang Đăng nhập trên Web Setup.
