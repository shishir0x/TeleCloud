# 🐳 Docker Deployment

This is the recommended method for servers. The Docker image (Alpine-based) includes **FFmpeg and yt-dlp built-in**.

---

## 🇺🇸 English

### Method 1: Single Container (Quick)

1. Pull the image: `docker pull ghcr.io/dabeecao/telecloud-go`
2. Prepare the directory and `.env`:
   ```bash
   mkdir telecloud && cd telecloud
   curl -O https://raw.githubusercontent.com/dabeecao/telecloud-go/main/env.example
   mv env.example .env
   # Fill in LOG_GROUP_ID in .env
   ```
3. Start the container:
   ```bash
   mkdir -p data && sudo chmod 777 data
   sudo docker run -d \
       --name telecloud \
       --restart unless-stopped \
       -p 8091:8091 \
       -v "$(pwd)/data:/app/data" \
       --env-file .env \
       -e DATABASE_PATH=/app/data/database.db \
       -e THUMBS_DIR=/app/data/thumbs \
       -e TEMP_DIR=/app/data/temp \
       --user 65532:65532 \
       ghcr.io/dabeecao/telecloud-go
   ```

### Method 2: Docker Compose (Recommended)

1. Download the configuration files:
   ```bash
   mkdir telecloud && cd telecloud
   curl -O https://raw.githubusercontent.com/dabeecao/telecloud-go/main/docker-compose.yml
   curl -O https://raw.githubusercontent.com/dabeecao/telecloud-go/main/env.example
   mv env.example .env
   ```
2. Fill in the required fields in `.env` (LOG_GROUP_ID...).
3. Start: `sudo docker compose up -d`

#### Useful Commands:
*   View logs: `sudo docker compose logs -f`
*   Stop: `sudo docker compose stop`
*   Update: `sudo docker compose pull && sudo docker compose up -d`
*   Remove container: `sudo docker compose down`

---

## 🇻🇳 Tiếng Việt

### Phương pháp 1: Chạy bằng lệnh Docker (Nhanh)

1. Tải image: `docker pull ghcr.io/dabeecao/telecloud-go`
2. Cấu hình thư mục và `.env`:
   ```bash
   mkdir telecloud && cd telecloud
   curl -O https://raw.githubusercontent.com/dabeecao/telecloud-go/main/env.example
   mv env.example .env
   # Điền LOG_GROUP_ID vào .env
   ```
3. Khởi động:
   ```bash
   mkdir -p data && sudo chmod 777 data
   sudo docker run -d \
       --name telecloud \
       --restart unless-stopped \
       -p 8091:8091 \
       -v "$(pwd)/data:/app/data" \
       --env-file .env \
       -e DATABASE_PATH=/app/data/database.db \
       -e THUMBS_DIR=/app/data/thumbs \
       -e TEMP_DIR=/app/data/temp \
       --user 65532:65532 \
       ghcr.io/dabeecao/telecloud-go
   ```

### Phương pháp 2: Docker Compose (Khuyên dùng)

1. Tải file cấu hình:
   ```bash
   mkdir telecloud && cd telecloud
   curl -O https://raw.githubusercontent.com/dabeecao/telecloud-go/main/docker-compose.yml
   curl -O https://raw.githubusercontent.com/dabeecao/telecloud-go/main/env.example
   mv env.example .env
   ```
2. Điền thông tin vào `.env` (LOG_GROUP_ID...).
3. Khởi động: `sudo docker compose up -d`

#### Các lệnh quản lý:
*   Xem log: `sudo docker compose logs -f`
*   Dừng: `sudo docker compose stop`
*   Cập nhật: `sudo docker compose pull && sudo docker compose up -d`
*   Xóa container: `sudo docker compose down`
