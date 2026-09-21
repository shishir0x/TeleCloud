# 🛠️ Development & Localization

Guide for developers and contributors who want to build TeleCloud from source or contribute translations.

---

## 🇺🇸 English

### 1. Build from Source

#### Method 1: Docker Build (Recommended)
Docker handles the whole build process without installing Go or Bun locally.
1. Clone the project: `git clone --recursive https://github.com/dabeecao/telecloud-go.git`
2. Build the image: `sudo docker build --build-arg DEFAULT_API_ID=your_api_id --build-arg DEFAULT_API_HASH=your_api_hash -t telecloud:local .`
3. Run the image you just built:
   ```bash
   sudo docker run -d -p 8091:8091 -v "$(pwd)/data:/app/data" --env-file .env telecloud:local
   ```

#### Method 2: Manual Build (Native)
1. Install **Golang (1.26+)** and **Bun** (https://bun.com).
2. Clone with `--recursive` (required to get the frontend code):
   `git clone --recursive https://github.com/dabeecao/telecloud-go.git`
3. Build the frontend:
   ```bash
   cd web
   bun install
   bun run build.js
   cd ..
   # Or simply run `make frontend` from the repository root
   ```
4. Build the backend:
   ```bash
   go mod tidy
   go build -o telecloud
   ```

##### Injecting default API ID & API Hash during a local build:
To avoid leaking sensitive API credentials into Git history, TeleCloud can load them dynamically from a local `.env` file (which is ignored by Git).
1. Define `API_ID` and `API_HASH` in your `.env` file.
2. Build using the helper tools:
   - **Using Makefile**: Run `make` or `make build`. It extracts the credentials from `.env` and embeds them.
   - **Using the test script**: Run `./build-test.sh`. It also reads `.env`, or prompts for input when the values are missing.
   - **Manually**:
     ```bash
     go build -ldflags="-X telecloud/config.DefaultAPIIDStr=YOUR_API_ID -X telecloud/config.DefaultAPIHash=YOUR_API_HASH" -o telecloud
     ```


### 2. Contributing Translations (Localization)

Frontend source lives in: [**dabeecao/telecloud-frontend**](https://github.com/dabeecao/telecloud-frontend).
1. Find the translation files in `static/locales/` (e.g. `vi.json`).
2. Create a new file (e.g. `fr.json`) and translate it from `en.json`.
3. Add the language to `availableLangs` in `static/js/common.js`.
4. Submit a Pull Request to the frontend repository.

Note: `en.json` is the source of truth. Run `npm run sync-locales` in `web/` after adding keys so every locale file stays in sync.

### 3. Database (SQL) Development & Encryption Key Guidelines

#### Encryption key (`master.key`):
- To prevent key path conflicts when the `data` directory is created after the application starts, all code resolves the key through `utils.GetMasterKeyFilePath()`.
- That helper automatically detects an existing key at `/app/data/master.key`, `data/master.key`, or next to the SQLite DB at `DATABASE_PATH`.
- During development, always call `utils.GetMasterKeyFilePath()` instead of building the path yourself, to avoid losing the key or generating a new one under MySQL/PostgreSQL.

#### Database compatibility rules:
- When writing SQL that touches logical/boolean columns (`BOOLEAN`), such as `is_folder` or `force_password_change`:
  - **DO NOT** assign or compare against integers (`1` or `0`).
  - **ALWAYS** use the standard SQL keywords `TRUE` and `FALSE` so the query works on SQLite, MySQL, and PostgreSQL's strict type checking.

---

## 🇻🇳 Tiếng Việt

### 1. Build từ nguồn

#### Phương pháp 1: Build bằng Docker (Khuyên dùng)
Docker xử lý toàn bộ quá trình build mà không cần cài đặt Go hay Bun trên máy.
1. Clone dự án: `git clone --recursive https://github.com/dabeecao/telecloud-go.git`
2. Build image: `sudo docker build --build-arg DEFAULT_API_ID=your_api_id --build-arg DEFAULT_API_HASH=your_api_hash -t telecloud:local .`
3. Chạy image vừa build:
   ```bash
   sudo docker run -d -p 8091:8091 -v "$(pwd)/data:/app/data" --env-file .env telecloud:local
   ```

#### Phương pháp 2: Build thủ công (Native)
1. Cài đặt **Golang (1.26+)** và **Bun** (https://bun.com).
2. Clone với `--recursive` (Bắt buộc để lấy code frontend):
   `git clone --recursive https://github.com/dabeecao/telecloud-go.git`
3. Build Frontend:
   ```bash
   cd web
   bun install
   bun run build.js
   cd ..
   # Hoặc đơn giản là chạy `make frontend` ở thư mục gốc
   ```
4. Build Backend:
   ```bash
   go mod tidy
   go build -o telecloud
   ```

##### Cách nhúng API ID & API Hash mặc định khi build cục bộ:
Để tránh bị lộ thông tin API nhạy cảm khi đẩy code lên GitHub, TeleCloud hỗ trợ tự động nạp thông tin này từ file `.env` cục bộ (được bỏ qua bởi Git).
1. Khai báo `API_ID` và `API_HASH` trong tệp `.env`.
2. Sử dụng các công cụ build:
   - **Bằng Makefile**: Chạy lệnh `make` hoặc `make build`. Script sẽ tự động lấy API từ tệp `.env` để nhúng lúc biên dịch.
   - **Bằng Script build test**: Chạy lệnh `./build-test.sh`. Script cũng sẽ tự đọc `.env`, hoặc hiển thị hộp thoại yêu cầu nhập nếu không tìm thấy.
   - **Bằng lệnh thủ công**:
     ```bash
     go build -ldflags="-X telecloud/config.DefaultAPIIDStr=YOUR_API_ID -X telecloud/config.DefaultAPIHash=YOUR_API_HASH" -o telecloud
     ```


### 2. Đóng góp bản dịch (Localization)

Mã nguồn frontend nằm ở repo: [**dabeecao/telecloud-frontend**](https://github.com/dabeecao/telecloud-frontend).
1. Tìm tệp bản dịch trong `static/locales/` (VD: `vi.json`).
2. Tạo tệp mới (VD: `fr.json`) và dịch từ `en.json`.
3. Thêm ngôn ngữ vào `availableLangs` trong `static/js/common.js`.
4. Gửi Pull Request vào repository frontend.

Lưu ý: `en.json` là nguồn chính. Sau khi thêm key mới, chạy `npm run sync-locales` trong thư mục `web/` để đồng bộ tất cả các tệp ngôn ngữ.

### 3. Lưu ý về phát triển Cơ sở dữ liệu (SQL) & Khóa mã hóa

#### Khóa mã hóa (`master.key`):
- Để tránh xung đột đường dẫn lưu trữ khóa `master.key` khi thư mục `data` được tạo sau lúc ứng dụng khởi động, toàn bộ mã nguồn sử dụng hàm `utils.GetMasterKeyFilePath()` để xác định vị trí tệp khóa chủ.
- Hàm này tự động phát hiện tệp khóa tồn tại sẵn tại các đường dẫn: `/app/data/master.key`, `data/master.key` hoặc gần file SQLite DB `DATABASE_PATH`.
- Trong phát triển, luôn gọi hàm `utils.GetMasterKeyFilePath()` thay vì tự nội suy đường dẫn để tránh lỗi mất khóa hay sinh khóa mới khi cấu hình MySQL/Postgres.

#### Quy tắc tương thích cơ sở dữ liệu:
- Khi viết các câu truy vấn SQL liên quan tới kiểu dữ liệu logic (`BOOLEAN`), ví dụ trường `is_folder` hoặc `force_password_change`:
  - **KHÔNG ĐƯỢC** gán hoặc so sánh trực tiếp với số nguyên (`1` hoặc `0`).
  - **BẮT BUỘC** sử dụng các từ khóa SQL tiêu chuẩn là `TRUE` và `FALSE` để đảm bảo tương thích tốt nhất trên cả SQLite, MySQL và đặc biệt là cơ chế so khớp kiểu dữ liệu nghiêm ngặt của PostgreSQL.
