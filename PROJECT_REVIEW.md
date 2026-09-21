# PROJECT REVIEW

**Verified against this checkout:** root repo `shishir0x/TeleCloud` (Go, `main.go` 586 lines, 20,362 Go LOC) + `web/` submodule `shishir0x/telecloud-frontend` (5,291-line `index.html`, 11,197-line `script.js`). Version string `v3.8.8` (Dockerfile ARG). Everything below is read from the code in this working tree, with file/line references. Where I infer rather than verify, I say so.

```text
PROJECT REVIEW
│
├── 1. Project Understanding
├── 2. Existing Features
├── 3. Current Architecture
├── 4. UI Audit
├── 5. UX Audit
├── 6. Mobile Audit
├── 7. Accessibility Audit
├── 8. Performance Audit
├── 9. Features To Add
├── 10. Features To Remove
├── 11. Features To Simplify
├── 12. User Retention Opportunities
├── 13. User Growth Opportunities
├── 14. Trust & Friendliness
├── 15. Information Architecture
├── 16. Priority Matrix
├── 17. Implementation Phases
├── 18. Dependencies & Risks
└── 19. Final Roadmap
```

---

## 1. Project Understanding

**What it is:** a self-hosted personal cloud drive that uses **Telegram as the storage backend**. Files upload to a Telegram account (Saved Messages or a log group); the app keeps an index in SQL (SQLite/MySQL/Postgres) and streams files back from Telegram with HTTP range support.

**Who uses it:** a single **admin** (the Telegram account owner) plus optional **child accounts** (sub-users with their own namespace, API key, WebDAV/S3 toggles).

**Entry points into the product:**

| Surface | Route | Auth |
|---|---|---|
| Web dashboard | `/` | session cookie (`api/router.go:230`) |
| Setup wizard | `/setup` | admin-only once configured (`middleware.go:120`) |
| Public share page | `/s/:token` | optional bcrypt share password |
| Direct file link | `/dl/:token` | derived token (`utils.GenerateDirectToken`) |
| WebDAV | `/webdav/*` | Basic Auth |
| S3-compatible API | `/s3/*` | SigV4 (`s3/auth.go`, 556 lines) |
| Upload API | `/api/upload-api/*` | `Bearer` key (`handlers_files.go:1363`) |
| Mobile app | external, linked in UI (`index.html:1124` → `telecloud-app.dabeecao.org`) | n/a |

**Core mental model (verified):**

```text
User (browser / WebDAV / S3 / API / mobile)
   ↓
Gin router + middleware chain
   securityHeaders → gzip → setupCheck → auth (cookie | Basic) → CSRF
   ↓
Handlers (api/*.go)
   ↓
database (sqlx: SQLite | MySQL | Postgres)          tgclient (gotd/td)
   files / file_parts / child_accounts / …      ←→  Telegram MTProto
   ↓                                                  (upload, stream, thumbs)
Local disk: temp chunks, thumbnails, master.key
```

**Important verified behaviours**

- **Setup gate:** if `admin_username` is empty, *everything* redirects to `/setup` (`middleware.go:105-115`). While Telegram is initializing, a hardcoded "TeleCloud is starting up" HTML page is served inline (`middleware.go:157-166`) — not a template, not themed, not translated.
- **Maintenance mode:** admin exists but Telegram not ready → non-admins get a plain-text 403 `"System is in maintenance mode."` (`middleware.go:178`).
- **Secrets encrypt themselves:** `TELECLOUD_MASTER_KEY` encrypts `api_id/api_hash/log_group_id/bot_tokens` and the Telegram session; if absent, a key file is generated and a schema-version migration exists (`database/migrate_encryption.go`).
- **Storage is free but fragile:** losing the Telegram account/session = losing all files; the app documents this but the *UI* never warns about it (see §14).
- **Fork drift:** the root push target is `shishir0x/TeleCloud` and the frontend submodule is `shishir0x/telecloud-frontend`, but the UI footer credits `@dabeecao` (`index.html:3522`) and the in-browser update check queries **`dabeecao/telecloud-go`** releases (`script.js:1981`). A fork user will be offered upstream updates.

---

## 2. Existing Features

Legend: ✅ works well · ⚠️ partial · 🟡 poor UX · 🔵 unnecessary · 🟣 improvable · 🔴 security/perf concern

### 2.1 Storage & file management

| Feature | What it does / where | Who | Value | Problems | Class |
|---|---|---|---|---|---|
| Folder tree + file list | `/api/files?path=` → tree UI (`index.html:1241+`) | all | core | loads a whole directory at once, no server paging | ✅ 🟣 🔴 |
| Upload (drag/drop, picker, paste, camera-less) | chunked POST, 50 MB chunks (`handlers_files.go:320-360`), resumable via `upload_chunks` | all | core | queue UI is complex; no per-file retry button (re-upload only) | ✅ 🟣 |
| Chunked/resumable upload | `upload_tasks`/`upload_chunks` + `chunkTrackerSync` | all | high on flaky networks | orphan temp cleanup only after 24 h (`main.go:462`) | ✅ |
| Remote URL upload | `/api/remote-upload` + `/check` (range/HTML warnings) | all | high | modal has its own cookie manager (yt-dlp cookies) — two different "cookies" concepts in one screen | ✅ 🟣 |
| yt-dlp tab | dedicated tab, formats picker, quality dropdown (`index.html:2553+`) | all | high for media hoarders | starts a separate tab; cookie UI duplicated with remote-upload | ✅ 🟣 |
| Torrent / magnet | aria2c tab (`handlers_torrent.go`) | all | medium | third tab for "download from internet" | 🟣 |
| Rename | `PUT /api/files/:id/rename` | owner | core | — | ✅ |
| Copy / Move | clipboard + `/api/actions/paste` | all | core | paste target = folder picker modal only (no "paste here in place") | ✅ 🟣 |
| Delete → Trash → Restore | soft delete, restore, permanent delete, purge after 30 days (`main.go:490`) | all | core | trash has **no bulk select**, only per-row or "empty all" | ✅ 🟣 |
| Batch download (zip on the fly) | `downloadSelectedBatch` + `handleDownloadFolder` | all | high | folder size limits unclear in UI | ✅ |
| Slideshow (images) | lightbox + slideshow controls | all | medium | discoverable only via a toolbar button that appears when images exist | 🟣 |
| Thumbnails | ffmpeg via `tgclient/thumbnail.go`, PDF/CBZ first page via poppler | all | high | regen is manual; N× `os.Stat` per listing | ✅ 🔴 |
| Comic reader (CBZ) | custom reader, direction/filter/scroll/fit/auto-scroll (`index.html:4189+`) | all | high | very toolbar-heavy | ✅ 🟡 |
| EPUB reader | epub.js-style resource proxy, TOC, themes, fonts, auto-scroll | all | high | another distinct toolbar vocabulary | ✅ |
| PDF reader | pdf.js, outline, zoom, print, download, progress bar | all | high | after this session's fixes: continuous scroll default, pinch zoom, seek bar | ✅ |
| Cinema/media player | Artplayer/Plyr, playlist, sub-bubbles, audio vinyl UI, keyboard n/p/space | all | high | huge amount of bespoke UI; playlist only for `isAudio`-style collections | ✅ 🟣 |

### 2.2 Sharing

| Feature | Detail | Class |
|---|---|---|
| Public link share | `/s/:token`, optional bcrypt password, HTML page with its own viewer (share.html 1,390 lines; share_folder.html 2,235) | ✅ |
| Folder share | browsable, per-file download, comic/PDF/EPUB readers inside share | ✅ |
| Direct link | `/dl/:token` derived token, no password (blocked when password set) | ✅ |
| Share viewer counters | `share_views`, `share_downloads` incremented server-side | ✅ |
| Unlock session | bcrypt verify → opaque `share_sessions` token, TTL | ✅ |
| **Share expiry** | **not implemented** — only password (`handlers_sharing.go:458-490`) | 🟣 missing |
| Share rate limiting | **none** on `/s/:token/verify` (`handlers_sharing.go:47-80`) — brute-forceable if the owner picks a weak password | 🔴 |

### 2.3 Accounts, access & integration

| Feature | Detail | Class |
|---|---|---|
| Single admin + child accounts | `child_accounts` table; per-user `api_key`, `webdav_enabled`, `api_enabled`, `force_password_change` | ✅ |
| Namespacing | `mapPath/unmapPath`; admin sees children as top-level folders (`handlers_files.go:150-207`) | ✅ (complex) |
| Session auth | `sessions` table, `session_token` cookie, logout-all on password change | ✅ |
| CSRF | double-submit cookie + `X-CSRF-Token`, bypass for `Authorization` requests (`middleware.go:55`) | ✅ |
| Login throttling | in-memory per-IP, 5 fails / 15 min, shared with Basic Auth (`middleware.go:44-52, 240-250`) | ✅ (in-memory only) |
| Passkeys (WebAuthn) | register/list/rename/delete + public login begin/finish (`api/passkey.go`) | ✅ |
| Force password change | middleware blocks every API except the password endpoint | ✅ |
| Password reset flow | `/reset-admin` with time-limited token setting | ✅ |
| WebDAV | full server incl. LOCK/UNLOCK, per-user enable + global toggle (`webdav/`) | ✅ |
| S3 API | SigV4 + CORS config, admin + child credentials | ✅ |
| Upload API | Bearer key, per-user, `/upload`, `/remote`, `/tasks/:id`, `/share` | ✅ |
| Bot pool | multiple bot tokens to parallelise upload/download + speed test (`handlers_settings.go`) | ✅ (advanced) |
| Backup/restore | dump/restore DB to Telegram, scheduled toggle (`tgclient/backup.go`, `restore.go`) | ✅ |
| Update check | browser → `api.github.com/repos/dabeecao/telecloud-go/releases`, shows changelog tab | 🟡 🔴 (points at upstream, third-party call) |
| Themes | 8+ presets, per-user via `/api/settings/user/theme`, dynamic CSS load | ✅ |
| i18n | 8 locales (`.min.json`), language picker, `t()` everywhere; **now defaults to English** | ✅ |
| Audit log | table + writes for login/logout/password/setup; **no read API or UI** | 🟡 |
| Health endpoints | `/healthz`, `/health` exist; docker-compose healthcheck commented out | 🟣 |

---

## 3. Current Architecture

**Strengths (verified):** clean Go package split (`api`, `database`, `tgclient`, `utils`, `webdav`, `s3`, `ws`, `config`); one place for routes; middleware order is correct (security headers → compression → setup gate → auth → CSRF); parameterised SQL throughout (`sqlx` with `?` binding); `share_password` never serialised (`json:"-"`, `database/db.go:37`); non-root container with `GOMEMLIMIT/GOGC` tuned for 512 MB hosts; yt-dlp binary SHA-256 verified at image build.

**Structural weaknesses**

| Problem | Evidence | Consequence |
|---|---|---|
| No frontend router | zero `pushState`/`hashchange`/`popstate` in `script.js` | tabs/folders are not linkable; browser **Back exits the app**; refresh always returns to the file root |
| One monolithic Alpine component | `cloudApp` in an 11,197-line file; `index.html` is 5,291 lines | any change risks unrelated regressions; hard to onboard contributors |
| Templates duplicated 3× | file-action menus appear in `index.html` (list *and* grid) and again in `share.html` / `share_folder.html` | fixes must be applied 4-6 times (as with this session's "Open in Tab") |
| Client-side everything | `filteredFiles`/`displayedFiles` slice in memory (`script.js:1572-1642`) | no server paging/search; big folders hurt |
| Dual cleanup of temp files | 24 h sweep (`main.go:462`) plus per-task logic | orphan temp files can linger a day |
| No CSP | `securityHeadersMiddleware` sets nosniff/frame/CORP/COOP only (`middleware.go:100-118`) | weaker XSS containment in a very rich UI |
| Tests are minimal & not in CI | 4 Go test files (`api`, `tgclient`, `utils`); `release.yml` has no `go test` | regressions ship |
| Documentation | `readme.md` (VN) + `readme_en.md` + 7 docs/*.md, but they lag the UI (e.g. no reader/share-expiry docs) | self-hosters guess |

---

## 4. UI Audit

**Positives:** coherent Tailwind design language (slate + blue, glassmorphism panels), consistent rounded corners and shadows, real empty states (`empty_folder` `index.html:3423`, `trash_empty` `:2982`, `no_results` `:3439`), good loading affordances (preloader, skeleton, indeterminate progress bar, `isRefreshing` spinner), destructive empty-trash action correctly colour-coded rose.

| Problem | Why it matters | Evidence |
|---|---|---|
| **Preloader is always dark** | Light-theme users see a hard black flash before the app paints | `index.html:34` `#app-preloader { background-color:#090d16 }` overrides `bg-slate-50 dark:bg-slate-950` on the element |
| **Every tab is a different visual language** | Files (list/grid) ↔ Trash (table) ↔ Users (custom) ↔ Settings (cards) ↔ YT-DLP ↔ Torrent ↔ Changelog: five header styles | `index.html:1241`, `:2880`, `:1299`, `:2553`, `:2798` |
| File info appears **three** times | Two menus in the dashboard (list + grid) and two more in share pages, with slightly different button sets | `index.html:3149`, `:3272`; `share.html:965`; `share_folder.html:2055` |
| Icon-only chrome with inconsistent affordances | Zoom/auto-scroll/fit icons are near-identical in the comic, EPUB and PDF toolbars but mean different things | `index.html:4221-4318`, `:4534-4640`, `:4873-4968` |
| Settings is one long card wall | 8 cards, no in-page nav or search; risky actions (Restart, Restore) live at the bottom of the same scroll | `index.html:1299-2550` |
| Terminal-style blocks for API docs | Six near-identical `<pre>` cURL samples | `index.html:1883-2050` |
| Typography is subtitle-light | Section headers use `text-xs uppercase tracking-wider` while card titles are bold-sm; hierarchy flattens on the mobile widths | e.g. `index.html:1246`, `:1305` |
| Mixed custom CSS + Tailwind | `style.css` is 41 KB hand-written beside Tailwind v4 | `web/static/css/style.css` |

---

## 5. UX Audit

### 5.1 First impression / new-user journey

Verified flow: `visit /` → redirect `/setup` → Telegram login (phone/QR/code/2FA) → Advanced API config → **Restart application** → login → dashboard.

| Step | Problem | Evidence |
|---|---|---|
| Setup wizard | 966-line single page, 4 sequential state machines (config, phone, QR, password) with no progress indicator or step count | `templates/setup.html` |
| Setup throws technical errors | Raw Telegram strings surface (`PEER_ID_INVALID`, `CHANNEL_INVALID`); one mapped key `setup_log_invalid` existed but was **missing from every locale** (fixed this session) | `setup.html:886` |
| Setup ends with "Restart" | The user's first completed action is *restarting a server* — no "you're ready, here's what to do next" | `setup.html:341+` |
| No onboarding in the app | After login the user lands in an empty root with no hint to upload/share/connect a client | `index.html` root empty state only |
| Mobile-app link goes off-site | Dashboard's second header control links to an external domain with no explanation | `index.html:1124` |
| "What is this?" | The dashboard never states that storage is your Telegram account | — |

### 5.2 Discoverability of existing features

Verified present but effectively hidden:

- **Trash** — only reachable from the mobile nav or the desktop tab bar.
- **Shared Links directory** — a toolbar button with a link icon; you must know it lists *your* shares.
- **Slideshow** — appears only when images exist in the current folder.
- **Slideshow/batch/zip, upload API, S3, WebDAV, passkeys, bot pool, backup, YT-DLP, torrent** — 9 capabilities, each behind a different entry point, none mentioned in the file list.
- **Keyboard shortcuts** exist only inside the cinema player (`n/p/space/arrows`, `script.js:1836`) — undiscoverable.
- **Search** — the magnifier is prominent but only matches the *current* folder's filenames (`script.js:1572-1577`), so users conclude "search is broken" (the common self-hosted-app complaint).

### 5.3 Flows, measured

| Flow | Steps today | Friction |
|---|---|---|
| Upload a file | open Upload → drag/select → wait (progress per file) → refresh | OK; no retry/pause control |
| Share a file | file menu → Share → toggle password → set password → Save → copy project link **or** copy direct link | 2 "copy" buttons with no explanation of when the direct link is unavailable (password on) |
| Stop sharing | Shared Links modal → find item → Revoke | no revoke from the file menu unless already shared |
| Delete → recover | select → Delete → Trash tab → Restore | no bulk restore, no "undo" toast |
| Move many files | select → Move → folder picker → navigate → Confirm | no drag-to-folder |
| Find a file | type in search (current folder only) | no global search, no filters by type/date/size |
| Preview vs download | preview blocked >50 MB with a clear notice (`share.html:240`) | dashboard has no equivalent pre-emptive size hint |
| Update | auto-check → Changelog tab → read GitHub-markdown-ish changelog | points at upstream repo |

Feedback quality is generally good: toasts (`showToast`), `handleCommonError`, confirm modals for destructive ops, per-task progress, WebSocket push with debounce (`script.js:2019`, `:2407-2419`). Missing: **undo**, and success toasts that say *what changed* ("3 files moved" is fine; "Done" is not — verify per-toast strings when implementing).

---

## 6. Mobile Audit

**What works:** a real bottom nav (`index.html:3529`), FAB with save-area-aware offsets (`:3629`, `:3802`), `pb-[calc(6.5rem+env(safe-area-inset-bottom))]` so content clears the nav (`:1274`), reader toolbars collapse into a "settings" drawer on mobile, `-webkit-overflow-scrolling: touch` and `overscroll-contain` in the readers.

| Problem | Why it matters | Evidence |
|---|---|---|
| **Zoom is globally disabled** | `maximum-scale=1.0, user-scalable=no` blocks pinch-zoom app-wide; only the readers implement their own | `index.html:5` |
| **Bottom nav is icon-only with up to 8 items** | Home, Trash, YT-DLP, FAB, Torrent, Users, Settings, Changelog → ~40 px targets, no labels, colour-only current state | `index.html:3529-3620` |
| Desktop-first file menus | The file-action popovers are shared with desktop; on a phone the grid/list menus stack into long sheets | `index.html:3149`, `:3272` |
| Multi-select bar floats above the nav | `bottom-[calc(7rem…)]` may collide with the FAB on small screens | `:3629` |
| Tab bar scrolls horizontally on desktop only | The desktop tab strip is a scrollable row; on mobile there is no equivalent for Settings sub-cards | `:1080-1120` |
| Hover-only affordances | Sorting icons use `opacity-0 group-hover:opacity-40` (invisible on touch) | `index.html:1258` |
| Reader pinch/double-tap | Works after this session's change; **not yet verified on a device** | `script.js` PDF handlers |
| Toasts/modals | Modals are large but scroll internally; verify keyboard overlap on iOS | — |
| No tablet breakpoint strategy | Only `sm/md/lg` used; mid-width tablets land in desktop layouts with touch input | global |

---

## 7. Accessibility Audit

| Problem | Evidence | Impact |
|---|---|---|
| Pinch-zoom blocked | `viewport maximum-scale=1.0, user-scalable=no` | WCAG 1.4.4 fail; low-vision users locked out |
| Almost no ARIA | 7 `aria-*` and 4 `role=` in 5,291 lines | Icon-only controls are unlabelled for screen readers |
| Icon-only buttons rely on `title` | 65 `:title` occurrences (better than nothing, not a substitute) | Announcement quality varies by AT |
| Nav has no `aria-current` | `:class` colour only | "Where am I?" is visual-only |
| Focus visibility unknown/patchy | Tailwind `focus:ring-*` used on inputs and some buttons (`:1230`), not on nav/tabs/toolbars | Keyboard users lose position |
| Tabs are `<button>`s without `role="tablist"`/`aria-selected` | `:1080-1120` | Semantics missing |
| Modals lack focus trap / `aria-modal` | All modals use `x-show` + overlay click-to-close | Focus escapes behind dialogs |
| No skip link | — | Keyboard users tab through the whole header/nav |
| Reversed colour choices | Comments mention `text-slate-455`/`slate-655`/`blue-105` (invalid Tailwind steps, silently dropped) | Low-contrast text where a real shade was intended (`index.html:5220`, `:5244`) |
| Motion | `transition-all duration-500` on theme switch, `animate-pulse`, marquee-ish shadows | No `prefers-reduced-motion` guard found |
| Language/AT | `<html lang>` now follows the UI language (this session) | ✅ improved |
| Table semantics (trash) | Table has no `<caption>`, no scope'd headers | Screen-reader table navigation weak (`:2925`) |

---

## 8. Performance & Technical UX Audit

No changes made — these are the identified risks.

| Problem | Evidence | User-visible effect |
|---|---|---|
| **Whole-directory fetch, `SELECT *`** | `handlers_files.go:162-182` | Large folder = slow load, big JSON, memory in both DB and browser |
| **Per-file `os.Stat` + MIME detection** | `handlers_files.go:210-236` | N syscalls per listing; the more files, the worse |
| Client-side pagination only | `itemsPerPage: 30` over the full in-memory array (`script.js:1566,1638`) | Pagination hides cost, doesn't remove it |
| No server-side search/index use | search filters in JS (`:1572`); DB has `idx_files_filename` unused for search | "Search" can't find files outside the folder |
| Heavy JS | `script.js` 11,197 lines → bundled + chunks; `chunk-MXNY6VMB.min.js` 1,834 lines loaded lazily; pdf.js, epub, Artplayer, Plyr, Prism each pulled on demand | First paint ok (preloader + preload hints), but media/reader entry is heavy |
| Third-party call on every dashboard load | `fetch('https://api.github.com/repos/…/releases')` (`script.js:1981`) | Privacy/offline latency; GitHub rate limits anonymous clients; fails on air-gapped installs |
| WebSocket + polling mix | `/api/ws` push (`:2019`) but several tabs also poll (`/api/tasks`, torrent status, yt-dlp status) | Redundant traffic; verify per-tab intervals |
| WS reconnect handling | `onclose`→reconnect exists (`:3553` comment) | Acceptable |
| Temp disk growth | chunk files removed after 24 h (`main.go:462`) | A failed 10 GB upload can pin disk for a day |
| Thumbnail generation blocking | ffmpeg per media file (`tgclient/thumbnail.go`) | On weak hosts, listing/upload feels slow |
| No HTTP/2 or CDN story | everything from one origin, `immutable` static caching already good (`router.go:26-32`) | Fine for self-host; heavy for mobile/remote |
| DB index coverage | good indices on `files(path)`, `(owner,path,filename)`, audit `(ts)`, `(actor)` | ✅ |
| `.min` artifacts gitignored & rebuilt at image build | `web/.gitignore` | Correct; but frontend fixes require a full image rebuild |

---

## 9. Features To Add

For each: **Problem → Why it matters → Solution → Benefit → Complexity → Dependencies → Priority.**

**F1. Global search (all folders)**
- **Problem:** the prominent search box only matches the current folder (`script.js:1572`).
- **Why:** the #1 "this app is broken" signal in file managers; users can't find their own files.
- **Solution:** `GET /api/search?q=&type=&from=&to=` using existing `idx_files_filename`/`idx_files_path`, scoped by `owner`/`mapPath`; results view with "reveal in folder".
- **Benefit:** the single biggest usability jump.
- **Complexity:** Medium (one query + one results view).
- **Dependencies:** none new.
- **Priority: Critical / High impact / Medium effort.**

**F2. Favorites + Recent**
- **Problem:** no way to pin or find recently touched files (`favorite`/`recent`: **0 occurrences** in the UI).
- **Why:** retention — a "my stuff" surface gives a reason to return.
- **Solution:** `favorites(username, file_id)` + `last_accessed_at` column; star action in the file menu; two sidebar/nav entries.
- **Benefit:** fastest path back to work in progress.
- **Complexity:** Low-Medium (migration + 3 routes).
- **Dependencies:** DB migration (SQLite/MySQL/PG variants exist as a pattern in `database/db.go`).
- **Priority: High / High / Low.**

**F3. Breadcrumb + tab deep links (routing)**
- **Problem:** no router; Back exits the app.
- **Solution:** `history.pushState` for `currentTab` + `currentPath` (`/#/files/docs`, `?path=`), plus `popstate` restore.
- **Benefit:** shareable URLs, working Back, refresh keeps position.
- **Complexity:** Medium (touches `navigateTo`, tab clicks).
- **Priority: High / High / Medium.**

**F4. Share expiry + per-link controls**
- **Problem:** shares live forever (`handlers_sharing.go:458-490`); no expiry UI.
- **Solution:** `share_expires_at`, optional max-downloads (columns `share_views/downloads` already exist), UI fields in the share modal, enforcement in `handleGetSharedFile`.
- **Benefit:** safe sharing is a trust feature.
- **Complexity:** Low-Medium.
- **Priority: High / High / Low.**

**F5. Trash bulk selection + undo toast**
- **Problem:** per-row actions or "empty all" only.
- **Solution:** reuse the existing `selectedIds` selection bar for trash; restore/delete-selected; 5-second Undo on delete.
- **Benefit:** fewer accidental permanent deletions.
- **Complexity:** Low.
- **Priority: Medium / Medium / Low.**

**F6. Audit-log viewer (admin)**
- **Problem:** `audit_log` is written (`database/audit.go`) but unreadable; uploads/deletes/shares aren't audited.
- **Solution:** extend `AuditAction` constants to file ops + `GET /api/audit?limit=&actor=` + an admin table with filters/CSV export.
- **Benefit:** security confidence for a self-hosted multi-user host.
- **Complexity:** Low-Medium.
- **Priority: Medium / Medium / Low.**

**F7. Storage/usage insights**
- **Problem:** `storage_used` exists; no breakdown.
- **Solution:** per-folder size, largest files, file-type donut from existing `size`/`mime_type`.
- **Benefit:** encourages cleanup; makes "unlimited" tangible.
- **Complexity:** Medium (aggregation queries).
- **Priority: Medium / Medium / Medium.**

**F8. Upload resilience UX**
- **Problem:** failed uploads resurface as whole-file re-uploads; chunk resume exists but isn't surfaced.
- **Solution:** per-item Retry/Pause, "resume from X%" using `upload_chunks` index, and "Copy failed list".
- **Benefit:** big uploads on mobile networks.
- **Complexity:** Medium.
- **Priority: Medium / Medium / Medium.**

**F9. Session/device management**
- **Problem:** `sessions` table has `created_at/expires_at`; no UI, no per-device logout.
- **Solution:** list active sessions (UA/IP already logged in audit), "sign out everywhere".
- **Complexity:** Low.
- **Priority: Medium / Medium / Low.**

**F10. Public-link analytics (owner-facing)**
- **Problem:** `share_views`/`share_downloads` are incremented but only shown to the *viewer* on the share page.
- **Solution:** show them in the Shared Links modal, with last-accessed time.
- **Complexity:** Low (fields already in `/api/files` payload).
- **Priority: Medium / Medium / Very low.**

**F11. PWA / installable + offline shell**
- **Problem:** a mobile app exists externally; the web app isn't installable.
- **Solution:** manifest + service worker caching the app shell (files stay network-only).
- **Complexity:** Low-Medium.
- **Priority: Medium / Medium(-High mobile) / Medium.**

**F12. Storage-integrity self-check**
- **Problem:** DB rows can reference Telegram messages that no longer exist (channel deleted, message removed); users discover this as a broken download.
- **Solution:** admin "verify library" job comparing `message_id` reachability, reporting/repairing dead rows.
- **Complexity:** High.
- **Priority: Medium-High (trust) / Medium / High.**

**F13. Experimental: shared spaces / multi-user folders, tag & smart collections, WOPI/office preview, per-folder share permissions, comment on share, "send to Telegram chat" button, Telegram-bot inline mode, versioning (keep previous `message_id`s).** Each needs validation; none are cheap. Recommend only after F1-F5 land.

---

## 10. Features To Remove

```text
Feature: Changelog tab driven by upstream GitHub releases
Current purpose: shows "update available" + last 5 releases
Why unnecessary: the repo is a fork (root shishir0x/TeleCloud, frontend shishir0x/telecloud-frontend)
  while the check queries dabeecao/telecloud-go (script.js:1981); it is also a third-party
  request from the browser on every load and fails on offline installs.
Downside of removing: self-hosters lose an update nudge.
Recommended action: CONSOLIDATE — move the check server-side, point it at the
  configured repo (or make it opt-in), and surface it in Settings.
```
```text
Feature: Duplicated file-info/action menus (4 copies)
Current purpose: list view, grid view, share.html, share_folder.html
Why unnecessary: identical markup, drifted button sets, 4 places to fix.
Recommended action: CONSOLIDATE into one partial per frontend.
```
```text
Feature: Cookie-management UI inside the YT-DLP/remote-upload modal
Current purpose: set cookies for restricted sources
Why unnecessary: it duplicates yt-dlp cookie handling in two tabs and confuses "browser cookie" with "yt-dlp cookie".
Recommended action: CONSOLIDATE into one "Download sources" area with a single cookie store.
```
```text
Feature: Third "download from the internet" tab (Torrent vs YT-DLP vs Remote URL)
Current purpose: three ways to fetch remote content
Why unnecessary: three tabs, three progress models, three failure vocabularies.
Recommended action: SIMPLIFY — one "Add download" entry with a source-type switcher (URL / magnet / torrent file).
```
```text
Feature: Hover-only sort affordances, invalid Tailwind shades (slate-455/655, blue-105)
Current purpose: styling
Why unnecessary/broken: invisible on touch; invalid classes are dropped by the compiler.
Recommended action: REMOVE / fix.
```
```text
Feature: Full-screen preloader for warm navigations
Current purpose: first-paint mask
Why unnecessary: it re-shows on tab switches/refresh with a 0.4 s fade and a hard dark background.
Recommended action: KEEP BUT REDESIGN (theme-aware, only on cold start).
```
```text
Feature: Raw Telegram error strings in the setup wizard
Recommended action: SIMPLIFY — map to human explanations (the mapping key exists; the pattern needs extending).
```

---

## 11. Features To Simplify

| Area | Today | Proposed | Action |
|---|---|---|---|
| Settings page | 8 stacked cards, no nav | Sectioned nav (Account / Security / Integrations / Data / Advanced) with the risky cards last and collapsed | SIMPLIFY |
| Reader toolbars (3 readers) | ~10 inline controls + a mobile drawer each | Shared component with a consistent icon set and one overflow menu | SIMPLIFY |
| Mobile nav | up to 8 icon targets | 5 max: Files, Search, +, Recent/Favorites, More (trash/torrent/yt-dlp/admin behind "More") | SIMPLIFY |
| Path handling | `mapPath/unmapPath` + admin-child folder magic (`handlers_files.go:150-207`) | Document the rule once and expose "owner" explicitly in the API response | SIMPLIFY |
| Error surfacing | mix of raw errors, `handleCommonError`, toasts, plain-text 403 pages | One error taxonomy rendered in the UI language | SIMPLIFY |
| Bot pool | upload-folder field, status badges, verify button | One "Test configuration" action that reports pass/fail per bot | SIMPLIFY |

---

## 12. User Retention Opportunities

Verified today, the returning-user loop is weak: no recent, no favorites, no notifications, no activity feed (`recent`/`activity`/`favorite` = 0 hits in the dashboard).

1. **Recent + Favorites (F2)** — the two surfaces that create a daily habit.
2. **Real global search (F1)** — the reason to open the app when a file is needed.
3. **Share analytics (F10)** — returning to check link activity.
4. **Storage insights (F7)** — weekly cleanup habit.
5. **Notifications (in-app only, no dark patterns):** "Upload finished for X", "Share Y was downloaded" (the WS hub already exists — use it).
6. **Upload resilience (F8)** — fewer rage-abandons.
7. **Continue-where-you-left-off:** remember last folder/tab (needs F3), and last-read page in EPUB/PDF (readers already track position in memory — persist it per file).
8. **Better empty states with verbs:** the root empty state should offer Upload / Remote URL / Connect WebDAV, not just "empty folder".
9. **PWA (F11)** for home-screen return on mobile.

---

## 13. User Growth Opportunities

1. **A 30-second first-run success:** after setup, land on the dashboard with a sample "Your first upload" checklist (upload → share → open share link). Right now setup ends at a server restart (`setup.html:341+`), which is the highest-risk drop-off point.
2. **Make the value crisp:** the dashboard never says *"your storage is your Telegram account; nothing leaves your hardware except the file blobs."* One line in the header/empty state converts self-hosters.
3. **Share pages are the growth surface:** `share.html`/`share_folder.html` are seen by *non-users*. Add a tasteful footer ("Powered by … — self-host your own") and keep the professional readers they already have. This is product-led growth with no dark patterns.
4. **Client integration as growth:** Upload API, WebDAV and S3 already exist — surface ready-to-copy config (rclone, Windows/macOS/Android WebDAV) at the moment of first upload, not buried in Settings.
5. **Fix fork identity first:** footer credit `@dabeecao` and the upstream update check actively undermine a fork's own growth (users get pointed at another product).
6. **Document the readers:** CBZ/EPUB/PDF readers are a genuine differentiator vs "just a file uploader"; nothing in the readme's feature list (line 40 ff.) markets them as a reading experience.
7. **i18n is a head start:** 8 locales exist. Growth in those markets needs the language default to be predictable (now English by default) plus correct locale files — no more hardcoded strings.

---

## 14. Trust & Friendliness

| Problem | Evidence | Recommended fix |
|---|---|---|
| Raw technical errors to end users | Setup surfaces `PEER_ID_INVALID`/`CHANNEL_INVALID` (`setup.html:886`); maintenance mode is plain text `"System is in maintenance mode. Only admin can access."` (`middleware.go:178`) | Map to human messages in the active language + a "what to do" line |
| No warning that storage == Telegram account | Throughout the UI | One-time dismissible banner in Settings + share of the risk in the backup card |
| Backup discoverability | Backup/restore card sits low in Settings (`index.html:2340`) | Promote: "Last backup: never — set one up" status chip in the header/empty state |
| No undo | Delete is immediate (trash is a good mitigation, but the toast doesn't offer Undo) | Undo toast for delete/rename/move |
| Share password brute force | no rate limit on `/s/:token/verify` (`handlers_sharing.go:47`) | Per-IP/share-token throttle (reuse the login limiter pattern) |
| No CSP | `middleware.go:100-118` | Add a CSP with nonces; the app already versions assets |
| Audit log invisible | §9 F6 | Admin viewer + extend audit to file ops |
| Session hygiene | no visible device list | F9 |
| Uptime signal | `/healthz` exists, healthcheck commented out (`docker-compose.yml:41-48`) | Enable by default |
| Maintenance/starting pages | inline hardcoded HTML, untranslated, unthemed (`middleware.go:157-166`) | Move to templates with the app's design |
| "Unlimited storage" claim | readme marketing | Qualify in-product: limits come from Telegram, not this app |

---

## 15. Information Architecture

```text
CURRENT STRUCTURE

Header:  [Mobile app ↗] [Language] [Logout]
Tabs:    Files | Trash | YT-DLP | Torrent | Users(admin) | Settings | Changelog(when update)
Files:   Breadcrumb row → Search + view toggle → list/grid → selection bar
Settings: System info · Bot pool · Telegram receipt · WebDAV · S3 · Upload API ·
          Mobile app · Appearance · Passkeys · Password · DB & Backup
                       ↓
PROPOSED STRUCTURE

Primary nav (5 max):
  Files | Search | + (upload/remote/torrent) | Recent · Favorites | More
More:  Trash · Torrent · YT-DLP · Shared links · (admin) Users · Settings · Help

Header: [Global search] [Upload] [Notifications] [Theme] [Language] [Account ▾]
Account ▾: Profile · Security (sessions, passkeys, password) · Preferences · Logout

Settings (grouped, deep-linkable):
  Account & Preferences   → theme, language, appearance, mobile app
  Security                → password, passkeys, sessions, audit log(admin)
  Integrations            → WebDAV, S3, Upload API, bot pool
  Data                    → backup/restore, trash retention, storage insights
  Advanced (admin)        → Telegram API, log group, restart, update policy
  Help / Diagnostics      → system status, verify library, export logs

Rule of thumb applied:
  - The file manager owns "my content" actions.
  - Anything pulled from the internet (YT-DLP/Torrent/Remote) is one "Add download" entry.
  - Anything you configure once (S3/WebDAV/API) lives in Integrations, never beside the file list.
  - Risky/destructive admin actions are last, collapsed, and confirm-gated.
```

---

## 16. Priority Matrix

Impact (user value + UX + product), Effort (implementation size in this codebase). No arbitrary numeric scores.

| # | Item | Impact | Effort | Priority |
|---|---|---|---|---|
| F1 | Global search | Critical | Medium | **P0** |
| F3 | Router / deep links / Back | High | Medium | **P0** |
| A1 | Remove `user-scalable=no`, add labels/ARIA to nav & icon buttons | High | Very Low | **P0** |
| U1 | Onboarding after setup (first-run checklist + "storage is Telegram" line) | High | Low | **P0** |
| F2 | Favorites + Recent | High | Low-Medium | **P1** |
| F4 | Share expiry/limits + verify rate limit | High | Low-Medium | **P1** |
| U2 | Mobile nav reduction to 5 + labels | High | Low | **P1** |
| S1 | Share-verify throttling, CSP, audit UI | High | Low-Medium | **P1** |
| P1 | Server-side listing/search paging, drop per-file `os.Stat`, kill the per-load GitHub call | High | Medium | **P1** |
| F5 | Trash bulk + undo | Medium | Low | **P2** |
| U3 | Settings IA regrouping + linkable sections | Medium | Medium | **P2** |
| F6/F9/F10 | Audit viewer, sessions UI, share analytics | Medium | Low | **P2** |
| P2 | De-duplicate the 4 file menus & 3 reader toolbars | Medium | Medium | **P2** |
| F7 | Storage insights | Medium | Medium | **P2** |
| F8 | Upload retry/pause | Medium | Medium | **P2** |
| F11 | PWA | Medium | Low-Medium | **P3** |
| F12 | Library integrity check | Medium-High (trust) | High | **P3** |
| F13 | Experimental (shared spaces, versioning, tags…) | Unknown | High | **P4** |

---

## 17. Implementation Phases

**Phase 0 — Foundation (blocks later work)**
Router/deep links (F3); single source for the file-action menu component; error-taxonomy function; CSP + security headers; enable `/healthz` healthcheck; add `go test ./...` to the release workflow; agree the DB-migration pattern for the three engines.
→ Touches `web/static/js/script.js`, `web/templates/*`, `api/middleware.go`, `.github/workflows/release.yml`, `docker-compose.yml`.

**Phase 1 — UX cleanup**
Theme-aware preloader; accessibility pass (viewport, labels, `aria-*`, focus rings, modal focus trap); settings regrouping + linkable sections; mobile nav reduction; kill hover-only affordances and invalid Tailwind shades; empty-state verbs; maintenance/starting pages as real templates.
→ `index.html`, `style.css`, `common.js`, `middleware.go`, locales.

**Phase 2 — Core experience**
Global search (F1); favorites/recent (F2); trash bulk + undo (F5); share expiry (F4); upload retry (F8); owner-facing share analytics (F10).
→ `api/handlers_files.go`, `database/db.go`, new `api/handlers_search.go`, `web/templates/index.html`, locale keys.

**Phase 3 — Feature improvements**
Audit viewer (F6); sessions UI (F9); storage insights (F7); first-run checklist; integration "copy config" blocks (rclone/WebDAV/API).

**Phase 4 — Performance**
Server-side paging + `LIMIT/OFFSET` (or keyset) for large folders; drop the per-file `os.Stat` (derive `has_thumb` from DB + one directory listing); move the update check server-side and cache it; review fan-out of per-tab polling; tighter temp-file lifecycle; thumbnail generation queue.
→ `api/handlers_files.go`, `web/static/js/script.js`, `main.go`.

**Phase 5 — Mobile & accessibility**
Device testing matrix (iOS Safari, Android Chrome, in-app WebViews); touch target audit ≥44 px; reader gesture regression (pinch/double-tap/seek) on real devices; `prefers-reduced-motion`; tablet layout pass; PWA (F11).

**Phase 6 — Retention & growth**
Notifications from the existing WS hub; persist reader position per file; share-page footer/branding; marketing of the readers in readme + first-run; fork identity fixes (footer, update repo, mobile-app link).

**Phase 7 — Polish**
Visual consistency across the 5 tab layouts; icon set unification; copy pass on all toasts/errors; changelog/announcements page; documentation sync (readme + docs/*.md).

---

## 18. Dependencies, Risks, Rollback

| Phase | Files/components | Backend / DB / API | Migration | Risks | Testing | Rollback |
|---|---|---|---|---|---|---|
| 0 | `script.js`, `templates/*`, `middleware.go`, workflow | New `/api/search` stub optional; no schema | None | Router change can break `navigateTo` callers in 4 templates | Manual URL/back tests + existing Go tests | Revert commit; routing is additive |
| 1 | `index.html`, `style.css`, `common.js`, locales | None | None | Removing `user-scalable=no` changes reader gesture feel | Visual diff on 4 breakpoints; reader zoom regression | Revert; CSS/class-only |
| 2 | handlers, `db.go`, templates | New routes: `/api/search`, `/api/favorites`, share expiry enforcement | **Yes:** favorites table, `share_expires_at`, `last_accessed_at` for SQLite/MySQL/PG | Migration on existing installs; multi-engine divergence | Unit tests for search scoping & share expiry + migrate test on SQLite | Keep columns additive; disable new UI; old rows default NULL |
| 3 | admin UI, audit constants, settings | `/api/audit`, `/api/sessions` | Trash column if "expires in" added | Audit volume on busy hosts | Verify authz (admin-only) | Revert routes; tables already exist |
| 4 | `handlers_files.go`, `script.js`, `main.go` | Listing contract changes (paging params) | None | Breaking older clients/mobile app if response shape changes | Contract test on `/api/files`, mobile app regression | Version query params; keep `?path=` behaviour |
| 5 | templates, CSS, `script.js` | None | None | Touch gesture conflicts in readers | Real-device matrix | Revert CSS/JS |
| 6 | WS hub, templates, readme | New WS event types; reader-position endpoint | Yes (reader state, optional) | Notification spam | Manual | Feature-flag off |
| 7 | templates, locales, docs | None | None | Low | Visual + i18n key audit (`t()` key scan) | Revert |

**Cross-cutting risks:** the frontend is a **submodule**, so every UI phase needs a submodule commit + parent pointer update + image rebuild (templates/locales are `//go:embed`-ded). Three SQL dialects must stay in lockstep. `.min.*` artifacts are gitignored, so "it works locally" requires a rebuild before verification. No frontend test harness exists — introduce one (or accept manual device testing) before Phase 0 lands.

---

## 19. Final Roadmap

```text
NOW   (P0)  Accessibility blockers (viewport zoom, labels/ARIA) · router/deep links ·
            global search · post-setup onboarding
NEXT  (P1)  Favorites/Recent · share expiry + verify throttling · CSP · mobile nav to 5 ·
            server-side listing paging · kill per-load GitHub call
THEN  (P2)  Trash bulk + undo · settings IA · audit viewer · sessions UI · share analytics ·
            upload retry · de-duplicate menus/toolbars
LATER (P3)  Storage insights · PWA · library integrity check
BET   (P4)  Shared spaces · versioning · tags/smart collections · office preview
```

### Closing report against the 15 requested points

1. **Project summary:** self-hosted Telegram-backed cloud drive: Go/Gin backend, Alpine.js/Tailwind frontend, WebDAV/S3/Upload-API/mobile entry points, 3 DB engines, media readers (PDF/EPUB/CBZ/video/audio), sharing, child accounts, passkeys, bot pool, backup/restore.
2. **Strengths to preserve:** storage abstraction and streaming/range support; middleware chain and CSRF design; `share_password` never serialised; encrypted secrets at rest; Docker hardening (non-root, checksum-verified yt-dlp, memory tuning); reader feature depth; 8-language i18n; trash with 30-day retention and shown deletion dates; empty states and toasts that already exist.
3. **Major problems:** no client-side routing; client-side-only search and paging; monolithic 11k-line component with 4× duplicated menus; fork identity pointing at upstream releases; audit log written but unreadable; no share expiry or share-verify throttling; minimal/no CI tests.
4. **UX problems:** current-folder-only search presented as global; setup ends at a server restart with no onboarding; feature discovery depends on seven tabs; no undo; no favorites/recent.
5. **Missing features:** global search, favorites/recent, share expiry/limits, audit viewer, session management, storage insights, upload retry, PWA, integrity check.
6. **Remove/simplify:** upstream changelog fetch (consolidate), duplicated action menus (consolidate), dual cookie UIs (consolidate), three separate "download from internet" tabs (simplify), hover-only affordances and invalid Tailwind shades (remove), dark-only preloader (redesign).
7. **UI improvements:** theme-aware preloader; one visual system across tabs; consistent reader toolbars; settings card wall → grouped sections; fix invalid utility classes; unify icon semantics.
8. **UX improvements:** Back/refresh that keep context; global search results with "reveal in folder"; undo toasts; first-run checklist; labelled mobile nav; pre-emptive size warnings before preview attempts.
9. **Performance:** server-side paging + avoid `os.Stat` per file; cache/cancel the GitHub release call; review polling fan-out; tighter temp lifecycle; queue thumbnail generation.
10. **Mobile:** allow pinch-zoom app-wide; ≤5 nav targets with labels; touch-safe sorting/selection; device-level reader gesture verification; tablet layout pass; PWA.
11. **Accessibility:** ARIA/focus/labels for icon-only controls, `aria-current`, modal focus trapping, `prefers-reduced-motion`, table semantics, contrast fixes.
12. **Retention:** favorites/recent, notifications from the existing WS hub, persisted reader position, "continue where you left off", meaningful empty states, storage insights.
13. **Growth:** share pages as the public surface, self-host pitch in-product, ready-to-copy WebDAV/S3/rclone configs, marketing the readers, fork-identity fix, leverage the existing 8 locales.
14. **Security & trust:** add share-verify rate limiting; add CSP; surface the audit log; warn that files live in Telegram and back up the master key; make backup status visible; map raw Telegram errors to human language; enable the healthcheck.
15. **Prioritized roadmap:** as in the NOW/NEXT/THEN/LATER/BET block above.

> "Review and implementation plan completed. No project files were modified."
