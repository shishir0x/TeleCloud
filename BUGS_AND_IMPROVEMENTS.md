# TeleCloud — Bugs & Feature Improvement Roadmap

> **Audit date:** 2026-09-24  
> **Codebase:** `shishir0x/TeleCloud` (Go backend) + `shishir0x/telecloud-frontend` (web submodule)  
> **Version:** v3.8.8

---

## Table of Contents

1. [Confirmed Bugs (Code-Level)](#1-confirmed-bugs-code-level)
2. [Security Vulnerabilities](#2-security-vulnerabilities)
3. [Performance Bottlenecks](#3-performance-bottlenecks)
4. [UI/UX Issues](#4-uiux-issues)
5. [Feature Upgrades — Quick Wins](#5-feature-upgrades--quick-wins)
6. [Feature Upgrades — Medium Effort](#6-feature-upgrades--medium-effort)
7. [Feature Upgrades — Major](#7-feature-upgrades--major)
8. [Priority Matrix](#8-priority-matrix)

---

## 1. Confirmed Bugs (Code-Level)

### BUG-01: `panic()` in WebAuthn initialization crashes the server

| Field | Detail |
|---|---|
| **File** | `api/passkey.go:70` |
| **Severity** | 🔴 Critical |
| **Description** | If WebAuthn config is invalid (e.g. bad RPID or empty origins), a bare `panic(err)` kills the entire process. Gin's `Recovery()` middleware only catches panics inside HTTP handlers — this one runs during init. |
| **Fix** | Return the error gracefully and disable passkey functionality instead of crashing. Log a warning and set `webAuthn = nil` so the rest of the app keeps running. |

### BUG-02: Login rate-limiter double-increments failed attempts

| Field | Detail |
|---|---|
| **File** | `api/handlers_auth.go:49-101` |
| **Severity** | 🟡 Medium |
| **Description** | On a failed login, the code checks the rate limit at line 49, but then increments `att.count` again at line 99 — after the initial `bumpAttempt` already runs for `/setup`. More importantly, the counter at lines 49-57 reads the old `att`, but the store at line 101 writes a brand-new `loginAttempt{count: att.count+1}`. This means the lockout triggers after **4** attempts, not the documented **5**. |
| **Fix** | Consolidate to a single `bumpAttempt(ip)` call and use the returned count to decide the response. |

### BUG-03: Stale `chunkTrackerSync` entries never evicted

| Field | Detail |
|---|---|
| **File** | `api/middleware.go:29` (declaration) + `api/handlers_files.go:358` (usage) |
| **Severity** | 🟡 Medium |
| **Description** | `chunkTrackerSync` is a `sync.Map` that grows for every upload task, but entries are never deleted after upload completes. On a long-running server with many uploads, this is a memory leak. |
| **Fix** | Call `chunkTrackerSync.Delete(taskID)` after a successful or failed upload finalization. |

### BUG-04: `loginAttempts` never cleaned up for legitimate IPs

| Field | Detail |
|---|---|
| **File** | `api/middleware.go:30` |
| **Severity** | 🟢 Low |
| **Description** | On successful login, the entry is deleted (line 86). But IPs that never succeed (e.g. scanners hitting `/login` but never logging in) remain in memory forever, slowly leaking RAM. |
| **Fix** | Add a periodic goroutine to sweep entries older than 15 minutes. |

### BUG-05: `handleGetFiles` leaks internal SQL errors to client

| Field | Detail |
|---|---|
| **File** | `api/handlers_files.go:184` |
| **Severity** | 🟡 Medium (security) |
| **Description** | `c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})` sends the raw Go/SQL error string to the client. This can leak table names, column names, or DB driver details. |
| **Fix** | Log the full error server-side and return a generic `"internal_error"` message to the client. |

### BUG-06: Self-restart doesn't work on Windows

| Field | Detail |
|---|---|
| **File** | `main.go:78-81` |
| **Severity** | 🟢 Low |
| **Description** | `executeRestart()` logs "Self-restart not supported on Windows" and calls `os.Exit(0)`. The "Restart" button in Settings will effectively shut down the application on Windows with no recovery. |
| **Fix** | Clearly communicate this limitation in the UI when running on Windows, or implement a watchdog/wrapper script that restarts the binary. |

### BUG-07: Update checker points at upstream `dabeecao/telecloud-go`

| Field | Detail |
|---|---|
| **File** | Frontend `script.js:1981` |
| **Severity** | 🟡 Medium |
| **Description** | The in-browser update check fetches from `api.github.com/repos/dabeecao/telecloud-go/releases`. For a fork, this will offer upstream updates that may be incompatible. |
| **Fix** | Change the repo to `shishir0x/TeleCloud` or make it configurable. |

### BUG-08: Footer credits `@dabeecao` on a fork

| Field | Detail |
|---|---|
| **File** | Frontend `index.html:3522` |
| **Severity** | 🟢 Low (cosmetic) |
| **Description** | The footer still shows the original developer credit. While proper attribution is important, it can confuse users about who maintains this fork. |
| **Fix** | Add "Fork maintained by @shishir0x" alongside the original credit. |

---

## 2. Security Vulnerabilities

### SEC-01: No rate limiting on share password verification

| Field | Detail |
|---|---|
| **Route** | `POST /s/:token/verify` |
| **File** | `api/handlers_sharing.go:47-85` |
| **Severity** | 🔴 High |
| **Description** | Share password verification has no rate limiting. An attacker who knows a share token can brute-force the password with unlimited attempts. The `bcrypt` cost provides some protection, but it's still brute-forceable with simple passwords. |
| **Fix** | Apply `loginAttempts`-style per-IP throttling to `handleVerifySharePassword`, or add a per-share-token attempt counter with exponential backoff. |

### SEC-02: No Content Security Policy (CSP)

| Field | Detail |
|---|---|
| **File** | `api/middleware.go:96-111` |
| **Severity** | 🟡 Medium |
| **Description** | `securityHeadersMiddleware` sets `X-Content-Type-Options`, `X-Frame-Options`, and CORP/COOP headers, but no `Content-Security-Policy`. The rich UI (pdf.js, epub.js, Artplayer, inline styles) makes a strict CSP hard, but even a report-only policy would catch injections. |
| **Fix** | Add a permissive-but-present CSP header. |

### SEC-03: Share sessions not cleaned up on share revocation

| Field | Detail |
|---|---|
| **File** | `api/handlers_sharing.go` |
| **Severity** | 🟡 Medium |
| **Description** | When a share link is revoked, the corresponding `share_sessions` rows are not deleted. Someone who already authenticated could theoretically keep using their session cookie until it expires (24h). |
| **Fix** | When revoking a share, also `DELETE FROM share_sessions WHERE share_token = ?`. |

### SEC-04: No password complexity requirements

| Field | Detail |
|---|---|
| **File** | `api/handlers_settings.go:21-79`, `api/handlers_auth.go:150-189` |
| **Severity** | 🟢 Low |
| **Description** | Neither password change nor admin reset enforces minimum length or complexity. A user can set password to "1". |
| **Fix** | Enforce minimum 8 characters at both API and UI level. |

### SEC-05: Reset token comparison is not constant-time

| Field | Detail |
|---|---|
| **File** | `api/handlers_auth.go:133` |
| **Severity** | 🟢 Low |
| **Description** | `token != dbToken` uses Go's standard string comparison which is not constant-time. A timing side-channel could theoretically leak the reset token byte-by-byte. |
| **Fix** | Use `subtle.ConstantTimeCompare([]byte(token), []byte(dbToken))`. |

---

## 3. Performance Bottlenecks

### PERF-01: `SELECT *` with per-file `os.Stat` on every directory listing

| Field | Detail |
|---|---|
| **File** | `api/handlers_files.go:162-236` |
| **Impact** | 🔴 High for large folders |
| **Description** | Every `GET /api/files?path=` fetches all columns for all files in a directory, then loops over each file to `os.Stat` the thumbnail path. With 500+ files this causes noticeable lag. |
| **Fix** | (1) Select only needed columns. (2) Add server-side pagination (`LIMIT/OFFSET` or cursor-based). (3) Cache thumbnail existence in the DB (`has_thumb BOOLEAN`). |

### PERF-02: No server-side search — client filters in memory

| Field | Detail |
|---|---|
| **File** | Frontend `script.js:1572-1577` |
| **Impact** | 🟡 Medium |
| **Description** | The search bar only matches `filteredFiles` in the current folder. All files are already loaded in memory client-side. There's no `GET /api/search` endpoint. |
| **Fix** | Add a server-side `GET /api/search?q=&type=&limit=&offset=` endpoint using the existing `idx_files_filename` index. |

### PERF-03: GitHub API called on every dashboard load

| Field | Detail |
|---|---|
| **File** | Frontend `script.js:1981` |
| **Impact** | 🟢 Low (latency + privacy) |
| **Description** | `fetch('https://api.github.com/repos/dabeecao/telecloud-go/releases')` runs on every dashboard load, adding latency and leaking user IP to GitHub. Fails on air-gapped installs. |
| **Fix** | Move the check server-side (backend caches it for 24h), make it opt-in in Settings, and point at the correct fork repo. |

### PERF-04: Temp file cleanup only runs every 24 hours

| Field | Detail |
|---|---|
| **File** | `main.go:461-488` |
| **Impact** | 🟡 Medium |
| **Description** | Failed uploads leave temp files for up to 24 hours. A 10 GB failed upload pins disk for a full day. |
| **Fix** | (1) Clean up immediately on upload failure. (2) Reduce sweep interval to 1 hour. (3) Add an admin "clean temp" button in Settings. |

### PERF-05: Monolithic frontend JS (11,197 lines)

| Field | Detail |
|---|---|
| **File** | Frontend `script.js` |
| **Impact** | 🟢 Low (already lazy-loads readers) |
| **Description** | While reader libraries are lazy-loaded, the core Alpine component is one massive file. Code splitting would improve load times and maintainability. |
| **Fix** | Long-term: break into modules per feature (file-manager, settings, readers, sharing). |

---

## 4. UI/UX Issues

### UX-01: No frontend router — Back button exits the app

| Field | Detail |
|---|---|
| **Impact** | 🔴 Critical UX |
| **Description** | No `pushState`/`popstate` handling. Navigating into a folder or switching tabs doesn't update the URL. Browser Back exits the entire app. Refresh always returns to root. |
| **Fix** | Implement `history.pushState` for `currentPath` and `currentTab`. Handle `popstate` to navigate back within the app. |

### UX-02: Preloader is always dark-themed

| Field | Detail |
|---|---|
| **File** | Frontend `index.html:34` |
| **Impact** | 🟡 Medium (jarring flash) |
| **Description** | `#app-preloader { background-color: #090d16 }` shows a dark flash for light-theme users. |
| **Fix** | Use `prefers-color-scheme` media query or inherit from the saved theme cookie. |

### UX-03: Pinch-zoom globally disabled

| Field | Detail |
|---|---|
| **File** | Frontend `index.html:5` |
| **Impact** | 🟡 Medium (accessibility violation: WCAG 1.4.4) |
| **Description** | `maximum-scale=1.0, user-scalable=no` blocks zoom app-wide. Only readers implement their own zoom. |
| **Fix** | Remove `maximum-scale=1.0, user-scalable=no` from the viewport meta tag. |

### UX-04: Setup wizard has no progress indicator

| Field | Detail |
|---|---|
| **File** | Frontend `templates/setup.html` (966 lines) |
| **Impact** | 🟡 Medium |
| **Description** | The setup wizard has 4 sequential state machines with no step count or progress bar. |
| **Fix** | Add a step indicator (e.g., "Step 2 of 4") at the top of the setup page. |

### UX-05: No onboarding after first login

| Field | Detail |
|---|---|
| **Impact** | 🟡 Medium |
| **Description** | After completing setup, the user lands on an empty root with no guidance. |
| **Fix** | Show a first-use overlay ("Welcome! Drag files here to upload..."). |

### UX-06: Search only works in the current folder

| Field | Detail |
|---|---|
| **Impact** | 🔴 Critical UX |
| **Description** | The search bar suggests global search but only filters the current folder. Users think search is broken. |
| **Fix** | Implement global search (see PERF-02). |

### UX-07: Inconsistent tab/view visual language

| Field | Detail |
|---|---|
| **Impact** | 🟢 Low (polish) |
| **Description** | Files uses list/grid, Trash uses a table, Users has its own layout, Settings is a card wall. 5+ different header styles. |
| **Fix** | Standardize on a consistent header + content layout pattern. |

### UX-08: Settings is one long card wall

| Field | Detail |
|---|---|
| **Impact** | 🟢 Low |
| **Description** | 8 cards with no navigation. Dangerous actions at the bottom of the same scroll. |
| **Fix** | Add in-page navigation within Settings. Separate "Danger Zone" section. |

### UX-09: Bottom nav has icon-only items with no labels

| Field | Detail |
|---|---|
| **Impact** | 🟡 Medium (mobile) |
| **Description** | Up to 8 icon-only items with ~40px targets, no text labels. |
| **Fix** | Add short text labels below each icon. Limit to 5 primary items. |

### UX-10: No undo for destructive actions

| Field | Detail |
|---|---|
| **Impact** | 🟡 Medium |
| **Description** | Deleting files, revoking shares, and emptying trash have no "undo" option. |
| **Fix** | Add a 5-second "Undo" toast for delete/move operations. |

---

## 5. Feature Upgrades — Quick Wins

> Estimated effort: **< 1 day each**

| # | Feature | What to do | Benefit |
|---|---|---|---|
| Q1 | **Fix fork references** | Update `script.js:1981` repo URL + `index.html:3522` footer | Correct identity |
| Q2 | **Share expiry field** | Add `share_expires_at` column + UI date picker + enforce in handler | Safe sharing |
| Q3 | **Trash bulk select** | Reuse existing `selectedIds` multi-select bar for trash tab | Fewer accidental deletes |
| Q4 | **Session/device listing** | `GET /api/sessions` → admin UI table with "sign out everywhere" | Security transparency |
| Q5 | **Share analytics for owner** | Show `share_views`/`share_downloads` in Shared Links modal | Link performance insights |
| Q6 | **Clean up chunkTracker entries** | `Delete(taskID)` after upload complete/error | Fix memory leak |
| Q7 | **Remove viewport zoom lock** | Delete `maximum-scale=1.0, user-scalable=no` | Accessibility fix |
| Q8 | **Rate-limit share passwords** | Add per-IP/per-token throttling on `POST /s/:token/verify` | Security fix |

---

## 6. Feature Upgrades — Medium Effort

> Estimated effort: **1–3 days each**

| # | Feature | What to do | Benefit |
|---|---|---|---|
| M1 | **Global search** | New `GET /api/search` endpoint + results UI with "reveal in folder" | #1 usability improvement |
| M2 | **Frontend URL routing** | `history.pushState` for tabs/folders + `popstate` handler | Working Back button |
| M3 | **Server-side pagination** | `LIMIT/OFFSET` in `handleGetFiles` + infinite scroll | Large folder performance |
| M4 | **Favorites & Recent** | `favorites` table + `last_accessed_at` column + nav entries | Quick access |
| M5 | **Upload resume UX** | Per-file Retry/Pause buttons using `upload_chunks` | Mobile reliability |
| M6 | **Theme-aware preloader** | Read theme from cookie to set preloader colors | Polish |
| M7 | **Onboarding flow** | First-use overlay with feature cards | Reduce time-to-value |
| M8 | **PWA manifest + service worker** | App shell caching, "Add to Home Screen" | Installable web app |
| M9 | **Settings navigation** | Sidebar/tabs within settings, "Danger Zone" section | Organization |
| M10 | **Audit log viewer** | `GET /api/audit` + admin table with filters/CSV export | Security visibility |

---

## 7. Feature Upgrades — Major

> Estimated effort: **3+ days each**

| # | Feature | What to do | Benefit |
|---|---|---|---|
| L1 | **Storage integrity checker** | Admin job checking `message_id` reachability on Telegram | Data safety |
| L2 | **Modular frontend** | Break `script.js` into ES modules per feature | Maintainability |
| L3 | **Content Security Policy** | Proper CSP header | XSS containment |
| L4 | **File versioning** | Keep previous `message_id`s, add "Version History" UI | Data recovery |
| L5 | **Shared spaces** | Cross-user folder access with permissions | Collaboration |
| L6 | **Mobile redesign** | Tablet breakpoints, touch menus, labeled nav | Better mobile UX |
| L7 | **Unified download view** | Merge YT-DLP, Remote URL, Torrent into one screen | Simpler UX |

---

## 8. Priority Matrix

```
                        HIGH IMPACT
                            │
           ┌────────────────┼────────────────┐
           │                │                │
           │  UX-01 Router  │  BUG-01 Panic  │
           │  UX-06 Search  │  SEC-01 Share  │
           │  M1 Global     │   rate-limit   │
           │    Search      │  Q6 Memory     │
           │  M2 URL        │    leak fix    │
           │    Routing     │                │
  LOW ─────┼────────────────┼────────────────┼───── HIGH
  EFFORT   │                │                │  EFFORT
           │  Q2 Share      │  PERF-01       │
           │    Expiry      │  Server-side   │
           │  Q3 Trash      │    pagination  │
           │    Bulk Select │  L1 Storage    │
           │  Q7 Zoom fix   │    integrity   │
           │  Q8 Rate-limit │  L2 Modular    │
           │                │    frontend    │
           │                │                │
           └────────────────┼────────────────┘
                            │
                        LOW IMPACT
```

### Recommended Implementation Order

**Phase 1 — Critical Fixes** (Week 1)
1. BUG-01: Fix `panic()` in WebAuthn → graceful error
2. SEC-01: Rate-limit share password verification
3. Q6: Clean up `chunkTrackerSync` memory leak
4. Q7: Remove viewport zoom restriction
5. BUG-05: Stop leaking SQL errors to client
6. Q1: Fix fork repo references

**Phase 2 — Core UX** (Weeks 2–3)
1. M2: Frontend URL routing (Back button fix)
2. M1: Global search
3. Q2: Share expiry
4. Q3: Trash bulk select
5. UX-02: Theme-aware preloader

**Phase 3 — Power Features** (Weeks 4–6)
1. M3: Server-side pagination
2. M4: Favorites & Recent
3. M5: Upload resume UX
4. Q4: Session management UI
5. M10: Audit log viewer

**Phase 4 — Polish & Scale** (Months 2–3)
1. M8: PWA support
2. M9: Settings reorganization
3. L6: Mobile-responsive redesign
4. L7: Unified download view
5. L3: Content Security Policy

---

> **Note:** This document is based on static code analysis. Runtime testing on actual devices is recommended to validate all findings, especially mobile UX issues and performance bottlenecks.
