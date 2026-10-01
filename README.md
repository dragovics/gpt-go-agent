# gpt-go-agent

Gateway eksekusi self-hosted yang memberikan klien MCP yang kompatibel dengan ChatGPT sebuah antarmuka kecil dan bisa di-audit ke workspace yang Anda kontrol.

Dapat berjalan dalam dua mode independen:

1. **MCP gateway** — mode default. Mengekspos tool file workspace dan tool perintah native terbatas secara opsional.
2. **Webhook worker** — opsional. Menerima job terautentikasi, meminta Middleman LLM menerjemahkan intent menjadi rencana eksekusi, lalu menerapkan kebijakan eksekusi deterministik sebelum menjalankan perintah native atau Codex.

Proyek ini dirancang untuk orang yang ingin klien AI bekerja terhadap VPS privat, development box, atau repositori terkontrol **tanpa mengekspos shell tanpa autentikasi untuk tujuan umum**.

---

## Daftar isi

- [Tujuan](#tujuan)
- [Apa proyek ini dan apa yang bukan](#apa-proyek-ini-dan-apa-yang-bukan)
- [Arsitektur](#arsitektur)
- [Ringkasan fitur](#ringkasan-fitur)
- [Model keamanan](#model-keamanan)
- [Kebutuhan](#kebutuhan)
- [Mulai cepat: MCP-only](#mulai-cepat-mcp-only)
- [Memilih profil deployment](#memilih-profil-deployment)
- [Tool MCP](#tool-mcp)
- [Menguji endpoint MCP secara manual](#menguji-endpoint-mcp-secara-manual)
- [Mengaktifkan tulis](#mengaktifkan-tulis)
- [Mengaktifkan eksekusi perintah terbatas](#mengaktifkan-eksekusi-perintah-terbatas)
- [Menghubungkan klien MCP jarak jauh](#menghubungkan-klien-mcp-jarak-jauh)
- [Mode webhook worker](#mode-webhook-worker)
- [Eksekusi Codex](#eksekusi-codex)
- [Health, readiness, dan metrics](#health-readiness-dan-metrics)
- [Audit logging](#audit-logging)
- [Referensi konfigurasi](#referensi-konfigurasi)
- [Deployment systemd](#deployment-systemd)
- [Alur rilis dan deployment](#alur-rilis-dan-deployment)
- [Pengujian dan CI](#pengujian-dan-ci)
- [Troubleshooting](#troubleshooting)
- [Rekomendasi operasional](#rekomendasi-operasional)
- [Tindak lanjut arsitektur yang diketahui](#tindak-lanjut-arsitektur-yang-diketahui)

---

## Tujuan

Tujuan utama `gpt-go-agent` adalah menyediakan **batas eksekusi kecil** antara klien AI dan mesin yang Anda kontrol.

Kasus penggunaan tipikal terlihat seperti ini:

```text
Klien ChatGPT / MCP
        |
        | permintaan MCP terautentikasi
        v
  gpt-go-agent
        |
        +-- akses workspace ter-root
        +-- tulis file opsional
        +-- perintah native terbatas opsional
        +-- eksekusi terbatas
        +-- audit log
        |
        v
 VPS / repositori / workspace privat
```

Daemon berguna saat Anda ingin klien AI untuk:

- memeriksa file di repositori privat;
- membuat atau memperbarui file di dalam satu workspace terkontrol;
- menjalankan sekumpulan kecil perintah native diagnostik;
- menyimpan jejak audit pemanggilan tool;
- menghindari mengekspos SSH atau raw shell langsung ke model;
- secara opsional menerima job asinkron durable melalui API webhook HTTP;
- secara opsional mendelegasikan job coding ke Codex dengan sandbox read-only atau workspace-write.

Deployment default sengaja konservatif:

- listener loopback;
- tulis file nonaktif;
- eksekusi perintah native nonaktif;
- eksekusi webhook nonaktif;
- tulis Codex nonaktif.

Anda secara eksplisit mengaktifkan kapabilitas tambahan.

---

## Apa proyek ini dan apa yang bukan

### Proyek ini adalah

- sebuah server HTTP MCP;
- gateway file dengan scope workspace;
- eksekutor perintah terbatas;
- batas audit;
- runner job asinkron durable opsional;
- jembatan eksekusi Middleman-ke-Codex/native opsional.

### Proyek ini bukan

- pengganti SSH;
- remote shell untuk tujuan umum;
- runtime container;
- sandbox OS lengkap;
- LLM yang disematkan di dalam server MCP;
- jaminan bahwa sembarang binary menjadi aman hanya karena nama executable-nya di-allowlist.

Pembedaan terkait eksekusi perintah itu penting.

`AGENT_ALLOWED_COMMANDS` hanya **gerbang pertama**. Sebuah perintah juga harus melewati kebijakan perintah terbatas deterministik yang diimplementasikan oleh server.

Misalnya, menambahkan `python3` ke `AGENT_ALLOWED_COMMANDS` **tidak** membuat `python3 -c ...` dapat dieksekusi lewat tool perintah terbatas MCP.

---

## Arsitektur

### Jalur MCP

```text
Klien ChatGPT / MCP
        |
        | POST /mcp
        v
+-------------------------+
|      gpt-go-agent       |
|-------------------------|
| Autentikasi bearer      |
| Workspace ter-root      |
| Kapabilitas baca/tulis  |
| Kebijakan cmd terbatas  |
| Batas timeout/output    |
| Sanitasi environment    |
| Audit logging           |
+------------+------------+
             |
             v
       host terkontrol
```

Operasi file MCP dilakukan melalui API filesystem ter-root milik Go. Ini mencegah sebuah path atau symlink di dalam workspace secara transparan ter-resolve ke luar root yang dikonfigurasi.

### Jalur webhook opsional

```text
Pemanggil webhook
      |
      | Token bearer
      | Idempotency-Key
      v
+----------------------+
| durable job queue    |
+----------+-----------+
           |
           v
+----------------------+
| Middleman planner    |
| OpenAI-compatible API|
+----------+-----------+
           |
           v
    execution decision
       /          \
      /            \
 native           codex
   |                |
   v                v
deterministic     Codex sandbox
command policy    read-only by default
   |                |
   +-------+--------+
           |
           v
      workspace
```

Middleman **bukan** otoritas keamanan final. Output-nya diperlakukan sebagai proposal eksekusi.

Perintah native tetap harus melewati kebijakan server-side deterministik sebelum child process dibuat.

---

## Ringkasan fitur

| Fitur | Default | Catatan |
|---|---|---|
| Endpoint HTTP MCP | aktif | `POST /mcp` |
| Listing file workspace | aktif | ter-root ke `AGENT_WORKSPACE` |
| Baca file workspace | aktif | ter-root dan output terbatas |
| Tulis file workspace | nonaktif | aktifkan dengan `AGENT_ALLOW_WRITE=1` |
| Eksekusi native MCP | nonaktif | aktifkan dengan `AGENT_ALLOW_COMMAND_EXEC=1` |
| Bearer token MCP | opsional untuk loopback read-only | wajib saat tulis/exec diaktifkan |
| Kebijakan native terbatas | selalu diterapkan | independen dari executable allowlist |
| Audit log | aktif | JSON Lines, mode 0600 |
| Webhook worker | nonaktif | `AGENT_WEBHOOK_ENABLED=1` |
| Autentikasi webhook | wajib saat diaktifkan | bearer token terpisah |
| Durable webhook jobs | aktif bersama webhook | JSON snapshot store + backup |
| Idempotency | aktif bersama webhook | `Idempotency-Key` |
| Retry Middleman | dapat dikonfigurasi | retry terbatas + circuit breaker |
| Eksekusi Codex | tersedia di mode webhook | butuh Codex CLI |
| Tulis workspace Codex | nonaktif | eksplisit `AGENT_CODEX_ALLOW_WRITE=1` |
| Endpoint health | aktif | `GET /healthz` |
| Endpoint readiness | aktif | `GET /readyz` |
| Endpoint metrics | aktif | `GET /metrics` |

---

## Memilih profil deployment

Bagian ini membantu memilih konfigurasi minimum sesuai kebutuhan.

### Profil minimal: MCP-only

Hanya untuk inspeksi file workspace.

Variabel minimum:

```bash
AGENT_WORKSPACE=/path/ke/workspace
AGENT_LISTEN_ADDR=127.0.0.1:8787
```

Cocok untuk:

- ChatGPT atau klien MCP yang hanya perlu membaca file di workspace privat
- Tidak butuh tulis, tidak butuh eksekusi perintah

### Profil menengah: tulis + eksekusi terbatas

Untuk pengeditan file dan perintah diagnostik kecil.

Variabel minimum:

```bash
AGENT_WORKSPACE=/path/ke/workspace
AGENT_LISTEN_ADDR=127.0.0.1:8787
AGENT_MCP_TOKEN='<random-hex-32>'
AGENT_ALLOW_WRITE=1
AGENT_ALLOW_COMMAND_EXEC=1
AGENT_ALLOWED_COMMANDS='echo uptime pwd date uname id ls'
```

Cocok untuk:

- Pengeditan terapeutik file (refactor, koreksi typo, tambah dokumentasi)
- Diagnostik server sederhana tanpa shell penuh

### Profil lengkap: webhook + Codex

Untuk job asinkron dan delegasi coding.

Variabel tambahan:

```bash
AGENT_WEBHOOK_ENABLED=1
AGENT_WEBHOOK_TOKEN='<random-hex-32>'
AGENT_MIDDLEMAN_URL=http://127.0.0.1:20128/v1
AGENT_MIDDLEMAN_MODEL=glm-5.3
AGENT_MIDDLEMAN_KEY='<...>'
AGENT_CODEX_BIN=codex
# AGENT_CODEX_ALLOW_WRITE=0  # biarkan read-only secara default
```

Cocok untuk:

- Antrian job durable dengan retry dan circuit-breaker
- Middleman menerjemahkan intent menjadi rencana eksekusi deterministik
- Codex CLI sebagai eksekutor untuk job coding dengan sandbox read-only

Prinsipnya: aktifkan kapabilitas hanya jika dipakai, dan selalu gunakan allowlist sekecil mungkin. Mode MCP-only tetap default sampai Anda secara eksplisit membutuhkan webhook atau Codex.

---

## Model keamanan

Model keamanan berdasarkan **beberapa gerbang independen**, bukan mempercayai keputusan model tunggal.

### 1. Eksposur jaringan

Listener default:

```text
127.0.0.1:8787
```

Tetap di loopback kecuali Anda secara sengaja mengonfigurasi autentikasi dan transport/tunnel aman.

### 2. Autentikasi MCP

Permintaan MCP jarak jauh butuh bearer token.

Saat token MCP tidak dikonfigurasi, permintaan tanpa token hanya diterima dari loopback dan hanya ketika server read-only/non-executing.

Jika salah satu ini diaktifkan:

```text
AGENT_ALLOW_WRITE=1
AGENT_ALLOW_COMMAND_EXEC=1
```

maka `AGENT_MCP_TOKEN` menjadi wajib saat startup.

Secret bearer dibandingkan menggunakan constant-time comparison.

### 3. Workspace ter-root

Semua operasi file di-scope ke:

```text
AGENT_WORKSPACE
```

Server menolak:

- path absolut;
- parent traversal keluar workspace;
- symlink traversal yang keluar workspace.

Contoh yang bukan akses workspace yang valid:

```text
/etc/passwd
../../etc/passwd
workspace-link -> /etc
```

### 4. Tulis bersifat opt-in

```text
AGENT_ALLOW_WRITE=0
```

adalah default.

Ini mengontrol tulis file MCP.

Tulis Codex dikontrol terpisah oleh `AGENT_CODEX_ALLOW_WRITE`.

### 5. Eksekusi native terbatas

Eksekusi perintah native mensyaratkan semua berikut:

1. `AGENT_ALLOW_COMMAND_EXEC=1`;
2. token MCP;
3. executable muncul di `AGENT_ALLOWED_COMMANDS`;
4. executable dan argumen melewati kebijakan terbatas deterministik.

Kebijakan terbatas saat ini memperbolehkan:

```text
echo
uptime
pwd
date
uname
id
ls
```

Kebijakan dengan sengaja menolak primitif eksekusi umum seperti:

```text
sh
bash
python
python3
node
npm
git
go
make
curl
wget
```

Menambahkan salah satu nama tersebut ke `AGENT_ALLOWED_COMMANDS` tidak melewati kebijakan deterministik.

### 6. Tidak ada string perintah shell

Eksekusi native memakai array argumen dengan `os/exec`. Ia tidak menggabungkan teks permintaan ke dalam `sh -c`.

### 7. Direktori kerja native tetap

Perintah native terbatas dimulai dari root workspace yang dikonfigurasi.

Nilai `cwd` kustom ditolak.

### 8. Sanitasi environment child

Variabel environment yang menyerupai credential dihapus dari proses child native.

Codex menggunakan jalur environment child sendiri karena secara sah mungkin memerlukan autentikasi/konfigurasi.

### 9. Eksekusi terbatas

Output native/Codex dibatasi.

Eksekusi perintah MCP juga punya timeout yang dapat dikonfigurasi.

### 10. Hardening systemd

Unit layanan yang disertakan menambahkan defense in depth dengan kontrol seperti:

- `NoNewPrivileges=true`;
- `ProtectSystem=strict`;
- `ProtectHome=true`;
- empty capability bounding set;
- pembatasan namespace;
- pembatasan device;
- pembatasan kernel/proc.

Hardening systemd adalah perlindungan tambahan. Ia tidak menggantikan kebijakan path dan eksekusi di level aplikasi.

---

## Kebutuhan

### Build

- Go 1.24 atau project toolchain yang kompatibel;
- Linux adalah target deployment utama.

### Opsional mode webhook/Codex

Tergantung konfigurasi:

- endpoint Middleman yang kompatibel dengan OpenAI;
- model Middleman/API key jika diperlukan endpoint tersebut;
- Codex CLI jika keputusan Middleman dapat mendispatch ke Codex.

Mode MCP-only dasar **tidak** memerlukan Middleman, Codex, atau webhook token.

---

## Mulai cepat: MCP-only

Clone dan build:

```bash
git clone https://github.com/dragovics/gpt-go-agent.git
cd gpt-go-agent

go build -o ./gpt-go-agent ./cmd/gpt-go-agent
```

Buat workspace:

```bash
mkdir -p /tmp/gpt-go-agent-workspace
printf 'hello from workspace\n' > /tmp/gpt-go-agent-workspace/hello.txt
```

Jalankan daemon dalam mode MCP read-only:

```bash
AGENT_WORKSPACE=/tmp/gpt-go-agent-workspace \
AGENT_LISTEN_ADDR=127.0.0.1:8787 \
./gpt-go-agent
```

Cek health:

```bash
curl -sS http://127.0.0.1:8787/healthz
```

Contoh respons:

```json
{"ok":true,"version":"0.2.1-dev"}
```

Cek readiness:

```bash
curl -sS http://127.0.0.1:8787/readyz
```

Contoh:

```json
{"ready":true}
```

---

## Tool MCP

### `list_dir`

Membuat daftar entry di bawah direktori relatif-workspace.

Input:

```json
{
  "path": "."
}
```

### `read_file`

Membaca file teks UTF-8 di bawah workspace.

Input:

```json
{
  "path": "README.md"
}
```

Output besar akan di-truncate ke batas output MCP yang dikonfigurasi.

### `write_file`

Menulis file UTF-8 di bawah workspace.

Membutuhkan:

```text
AGENT_ALLOW_WRITE=1
AGENT_MCP_TOKEN=<token>
```

Input:

```json
{
  "path": "notes/result.txt",
  "content": "finished\n"
}
```

Direktori parent dibuat di dalam workspace ter-root sesuai kebutuhan.

### `exec_command`

Menjalankan perintah native terbatas.

Membutuhkan:

```text
AGENT_ALLOW_COMMAND_EXEC=1
AGENT_MCP_TOKEN=<token>
AGENT_ALLOWED_COMMANDS=<allowlist>
```

Contoh input:

```json
{
  "command": "uptime",
  "args": []
}
```

Executable harus muncul di allowlist server yang dikonfigurasi **dan** diterima oleh kebijakan deterministik.

Direktori kerja kustom tidak didukung; perintah dijalankan dari root workspace.

---

## Menguji endpoint MCP secara manual

Implementasi MCP yang ditulis tangan saat ini menerapkan versi protokol:

```text
2025-06-18
```

### Initialize

Tanpa token dalam mode loopback read-only:

```bash
curl -sS http://127.0.0.1:8787/mcp \
  -H 'Content-Type: application/json' \
  --data '{
    "jsonrpc":"2.0",
    "id":1,
    "method":"initialize",
    "params":{}
  }'
```

Dengan autentikasi:

```bash
curl -sS http://127.0.0.1:8787/mcp \
  -H 'Authorization: Bearer ***' \
  -H 'Content-Type: application/json' \
  --data '{
    "jsonrpc":"2.0",
    "id":1,
    "method":"initialize",
    "params":{}
  }'
```

### List tools

```bash
curl -sS http://127.0.0.1:8787/mcp \
  -H 'Authorization: Bearer ***' \
  -H 'Content-Type: application/json' \
  --data '{
    "jsonrpc":"2.0",
    "id":2,
    "method":"tools/list",
    "params":{}
  }'
```

### Baca file

```bash
curl -sS http://127.0.0.1:8787/mcp \
  -H 'Authorization: Bearer ***' \
  -H 'Content-Type: application/json' \
  --data '{
    "jsonrpc":"2.0",
    "id":3,
    "method":"tools/call",
    "params":{
      "name":"read_file",
      "arguments":{"path":"hello.txt"}
    }
  }'
```

### Jalankan perintah terbatas

```bash
curl -sS http://127.0.0.1:8787/mcp \
  -H 'Authorization: Bearer ***' \
  -H 'Content-Type: application/json' \
  --data '{
    "jsonrpc":"2.0",
    "id":4,
    "method":"tools/call",
    "params":{
      "name":"exec_command",
      "arguments":{"command":"uptime","args":[]}
    }
  }'
```

---

## Mengaktifkan tulis

Set token MCP yang kuat dan aktifkan tulis secara eksplisit:

```bash
export AGENT_MCP_TOKEN='ganti-dengan-secret-random-yang-panjang'
export AGENT_ALLOW_WRITE=1
export AGENT_WORKSPACE=/var/lib/gpt-go-agent/workspace

./gpt-go-agent
```

Server menolak start dengan tulis diaktifkan dan token MCP kosong.

Secret sederhana dapat di-generate dengan tool lokal seperti:

```bash
openssl rand -hex 32
```

Perlakukan token sebagai password.

---

## Mengaktifkan eksekusi perintah terbatas

Contoh:

```bash
export AGENT_MCP_TOKEN='ganti-dengan-secret-random-yang-panjang'
export AGENT_ALLOW_COMMAND_EXEC=1
export AGENT_ALLOWED_COMMANDS='echo uptime pwd date uname id ls'

./gpt-go-agent
```

Allowlist dan kebijakan deterministik di-intersect.

Misalnya:

```bash
AGENT_ALLOWED_COMMANDS='python3 uptime'
```

tidak membuat Python dapat dieksekusi lewat MCP. `uptime` bisa lolos; `python3` ditolak oleh kebijakan deterministik.

Jika kebutuhan produk Anda sebenarnya adalah eksekusi kode jarak jauh sembarang, jangan melemahkan tool terbatas ini secara diam-diam. Perkenalkan mode eksekusi privileged dengan nama terpisah beserta asumsi trust dan deployment yang eksplisit.

---

## Menghubungkan klien MCP jarak jauh

Server default bind ke loopback.

Topologi yang direkomendasikan:

```text
Klien ChatGPT / MCP jarak jauh
        |
        | secure tunnel / private network
        v
127.0.0.1:8787 di VPS Anda
        |
        v
gpt-go-agent
```

Untuk deployment non-loopback:

- konfigurasikan `AGENT_MCP_TOKEN`;
- gunakan TLS atau secure tunnel tepercaya;
- jangan ekspos endpoint HTTP tanpa autentikasi langsung ke internet;
- batasi workspace ke file yang benar-benar dibutuhkan agent.

Setup klien MCP persisnya berbeda antar klien. Endpoint yang dikonfigurasi adalah:

```text
POST /mcp
```

dengan:

```http
Authorization: Bearer ***
```

saat autentikasi diaktifkan.

---

## Mode webhook worker

Mode webhook independen dari operasi MCP normal dan nonaktif secara default.

Aktifkan dengan:

```bash
export AGENT_WEBHOOK_ENABLED=1
export AGENT_WEBHOOK_TOKEN='ganti-dengan-secret-random-terpisah'

export AGENT_MIDDLEMAN_URL='http://127.0.0.1:20128/v1'
export AGENT_MIDDLEMAN_MODEL='glm-5.3'
export AGENT_MIDDLEMAN_KEY='...'

./gpt-go-agent
```

Saat diaktifkan, endpoint berikut di-mount:

```text
POST /webhook
GET  /webhook?limit=50&offset=0
GET  /webhook/{job-id}
```

Ketika mode webhook nonaktif, route ini tidak di-mount.

### Buat job

```bash
curl -sS -X POST http://127.0.0.1:8787/webhook \
  -H 'Authorization: Bearer YOUR_W...OKEN' \
  -H 'Content-Type: application/json' \
  -H 'Idempotency-Key: example-job-001' \
  -H 'X-Request-ID: local-test-001' \
  --data '{
    "intent":"check server uptime",
    "context":"diagnostic request"
  }'
```

Job baru biasanya mengembalikan HTTP `202 Accepted`.

Contoh respons:

```json
{
  "job_id": "job-...",
  "status": "pending"
}
```

### Poll job

```bash
curl -sS http://127.0.0.1:8787/webhook/JOB_ID \
  -H 'Authorization: Bearer YOUR_W...OKEN'
```

### List jobs

```bash
curl -sS 'http://127.0.0.1:8787/webhook?limit=50&offset=0' \
  -H 'Authorization: Bearer YOUR_W...OKEN'
```

Batasan pagination:

- limit default: 50;
- limit maksimum: 200;
- offset harus non-negatif.

### Idempotency

Jika `Idempotency-Key` yang sama dipakai ulang dengan konten permintaan yang sama, job yang sudah ada dikembalikan alih-alih membuat job baru.

Jika key yang sama dipakai ulang dengan konten berbeda, server mengembalikan HTTP `409 Conflict`.

Ini membuat retry dari pemanggil webhook lebih aman.

### Correlation ID

Kirim:

```http
X-Request-ID: your-correlation-id
```

ID disimpan bersama job dan disertakan di record audit.

Jika tidak ada request ID, worker akan men-generate satu.

### State job

Job dapat berpindah melalui state berikut:

```text
pending
evaluating
rejected
running
completed
failed
```

Siklus sukses tipikal:

```text
pending
   |
   v
evaluating
   |
   v
running
   |
   v
completed
```

Penolakan kebijakan:

```text
pending -> evaluating -> rejected
```

Kegagalan operasional:

```text
pending -> evaluating/running -> failed
```

Kegagalan Middleman yang dapat di-retry dapat mengembalikan job ke `pending` sebelum attempt terbatas lain.

### Durable store

Path default:

```text
/var/lib/gpt-go-agent/jobs/store.json
```

Store menggunakan:

1. file sementara;
2. `fsync` file;
3. rename primary ke backup;
4. rename temp ke primary;
5. `fsync` direktori.

Saat startup:

- primary yang valid lebih diutamakan;
- primary yang hilang/rusak fallback ke `.bak`;
- jika keduanya absen, store mulai kosong;
- jika keduanya ada tapi invalid, startup gagal, bukan diam-diam mengarang state.

Store ini cocok untuk workload menengah. Lihat [Tindak lanjut arsitektur yang diketahui](#tindak-lanjut-arsitektur-yang-diketahui) untuk arah storage volume tinggi.

---

## Eksekusi Codex

Keputusan webhook dapat mendispatch task coding ke Codex.

Codex **read-only secara default**:

```text
AGENT_CODEX_ALLOW_WRITE=0
```

Sandbox Codex efektif menjadi:

```text
read-only
```

Untuk secara sengaja memberikan mutasi workspace:

```bash
export AGENT_CODEX_ALLOW_WRITE=1
```

Sandbox efektif menjadi:

```text
workspace-write
```

Izin tulis Codex sengaja terpisah dari MCP `AGENT_ALLOW_WRITE`.

Artinya:

```text
AGENT_ALLOW_WRITE=0
AGENT_CODEX_ALLOW_WRITE=1
```

adalah konfigurasi yang valid: MCP tidak dapat memanggil `write_file`, sedangkan webhook executor Codex tetap boleh memodifikasi workspace-nya.

Demikian pula:

```text
AGENT_ALLOW_WRITE=1
AGENT_CODEX_ALLOW_WRITE=0
```

mengizinkan tulis file MCP terautentikasi sementara Codex tetap read-only.

---

## Health, readiness, dan metrics

### `GET /healthz`

Mengembalikan kesehatan proses dan versi:

```bash
curl -sS http://127.0.0.1:8787/healthz
```

Contoh:

```json
{"ok":true,"version":"0.2.1-dev"}
```

Build release menimpa versi development melalui linker flags.

### `GET /readyz`

Mode MCP-only siap setelah startup.

Mode webhook juga memeriksa apakah konfigurasi Middleman yang diperlukan tersedia.

```bash
curl -i http://127.0.0.1:8787/readyz
```

Layanan yang belum siap mengembalikan HTTP `503`.

### `GET /metrics`

Metrics gaya Prometheus-text diekspos di:

```bash
curl -sS http://127.0.0.1:8787/metrics
```

Dalam mode MCP-only, endpoint bisa kosong karena worker metrics tidak diregistrasi.

Mode webhook dapat mengekspos metrics seperti:

```text
gpt_go_agent_jobs_total
gpt_go_agent_queue_depth
gpt_go_agent_jobs_pending
gpt_go_agent_jobs_evaluating
gpt_go_agent_jobs_running
gpt_go_agent_jobs_completed
gpt_go_agent_jobs_failed
gpt_go_agent_jobs_rejected
gpt_go_agent_jobs_retried
gpt_go_agent_jobs_dead_lettered
```

Metrics Middleman digabung ke output yang sama saat tersedia.

---

## Audit logging

Path audit default:

```text
agent-audit.jsonl
```

Deployment produksi biasanya menyetel:

```text
AGENT_AUDIT_PATH=/var/lib/gpt-go-agent/agent-audit.jsonl
```

File dibuka dengan mode `0600`.

Tiap baris adalah objek JSON.

Event representatif:

```json
{
  "time":"2026-10-01T00:00:00Z",
  "action":"read_file",
  "target":"README.md",
  "allowed":true
}
```

Event webhook juga dapat berisi:

- correlation ID;
- detail keputusan;
- durasi;
- informasi kegagalan.

Timestamp audit dinormalisasi ke UTC.

---

## Referensi konfigurasi

### Core / MCP

| Variabel | Default | Tujuan |
|---|---|---|
| `AGENT_LISTEN_ADDR` | `127.0.0.1:8787` | alamat listen HTTP |
| `AGENT_WORKSPACE` | `.` | direktori root yang diekspos ke MCP/executor |
| `AGENT_MCP_TOKEN` | kosong | bearer token MCP |
| `AGENT_ALLOW_WRITE` | `0` | aktifkan MCP `write_file` |
| `AGENT_ALLOW_COMMAND_EXEC` | `0` | aktifkan eksekusi native MCP terbatas |
| `AGENT_ALLOWED_COMMANDS` | kosong kecuali dikonfigurasi | allowlist executable server-side |
| `AGENT_AUDIT_PATH` | `agent-audit.jsonl` | path audit JSONL |

Allowlist terbatas yang direkomendasikan:

```text
echo,uptime,pwd,date,uname,id,ls
```

### Webhook

| Variabel | Default | Tujuan |
|---|---|---|
| `AGENT_WEBHOOK_ENABLED` | `0` | mount/start webhook worker |
| `AGENT_WEBHOOK_TOKEN` | kosong | bearer token webhook |
| `AGENT_WEBHOOK_WORKERS` | `4` | jumlah worker background |
| `AGENT_WEBHOOK_STORE` | `/var/lib/gpt-go-agent/jobs/store.json` | durable job store |
| `AGENT_WEBHOOK_RETENTION` | `168h` | retensi job terminal |
| `AGENT_WEBHOOK_CLEANUP_INTERVAL` | `1h` | interval cleanup retensi |
| `AGENT_WEBHOOK_MAX_GATEKEEPER_RETRIES` | `2` | retry Middleman level worker |
| `AGENT_WEBHOOK_RETRY_BASE` | `500ms` | delay dasar retry worker |
| `AGENT_WEBHOOK_MAX_OUTPUT_BYTES` | `65536` | batas output native/Codex |

`OPENAI_WEBHOOK_SECRET` diterima sebagai fallback kompatibel-mundur untuk `AGENT_WEBHOOK_TOKEN`, tapi deployment baru sebaiknya pakai `AGENT_WEBHOOK_TOKEN`.

### Middleman

| Variabel | Default | Tujuan |
|---|---|---|
| `AGENT_MIDDLEMAN_URL` | `http://127.0.0.1:20128/v1` | base API kompatibel OpenAI |
| `AGENT_MIDDLEMAN_KEY` | kosong | API key Middleman |
| `AGENT_MIDDLEMAN_MODEL` | `glm-5.3` | identifier model |
| `AGENT_MIDDLEMAN_TIMEOUT` | `30s` | timeout request Middleman |
| `AGENT_MIDDLEMAN_MAX_ATTEMPTS` | `3` | jumlah attempt level transport |
| `AGENT_MIDDLEMAN_RETRY_BASE` | `250ms` | delay dasar retry |
| `AGENT_MIDDLEMAN_CIRCUIT_THRESHOLD` | `3` | jumlah kegagalan sebelum breaker terbuka |
| `AGENT_MIDDLEMAN_CIRCUIT_OPEN` | `10s` | durasi circuit terbuka |

### Codex

| Variabel | Default | Tujuan |
|---|---|---|
| `AGENT_CODEX_BIN` | `codex` | executable Codex |
| `AGENT_CODEX_ALLOW_WRITE` | `0` | ubah sandbox dari read-only ke workspace-write |

Contoh lengkap ada di:

```text
deploy/env.example
```

---

## Deployment systemd

Repositori menyertakan:

```text
deploy/gpt-go-agent.service
```

Service mengharapkan:

```text
User=gptagent
Group=gptagent
WorkingDirectory=/var/lib/gpt-go-agent
EnvironmentFile=/etc/gpt-go-agent/env
ExecStart=/usr/local/bin/gpt-go-agent
```

Persiapan host tipikal:

```bash
sudo useradd --system --home /var/lib/gpt-go-agent --shell /usr/sbin/nologin gptagent

sudo install -d -o gptagent -g gptagent -m 0700 \
  /var/lib/gpt-go-agent \
  /var/lib/gpt-go-agent/workspace \
  /var/lib/gpt-go-agent/jobs

sudo install -d -o root -g root -m 0755 /etc/gpt-go-agent
sudo install -o root -g root -m 0600 deploy/env.example /etc/gpt-go-agent/env

sudo install -o root -g root -m 0644 \
  deploy/gpt-go-agent.service \
  /etc/systemd/system/gpt-go-agent.service
```

Edit environment produksi:

```bash
sudo editor /etc/gpt-go-agent/env
```

Lalu:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now gpt-go-agent
sudo systemctl status gpt-go-agent
```

Log:

```bash
journalctl -u gpt-go-agent -f
```

Health dari host:

```bash
curl -fsS http://127.0.0.1:8787/healthz
curl -fsS http://127.0.0.1:8787/readyz
```

---

## Alur rilis dan deployment

Tag rilis mengikuti:

```text
vMAJOR.MINOR.PATCH
```

Workflow release GitHub membangun:

- Linux amd64;
- Linux arm64;
- checksum SHA256.

Helper deploy:

```text
scripts/deploy.sh
```

melakukan:

1. validasi executable;
2. install bertahap;
3. output SHA256;
4. backup binary sebelumnya;
5. stop/start systemd;
6. pengecekan `/healthz` dan `/readyz`;
7. rollback ke binary sebelumnya jika kesehatan deployment gagal.

Penggunaan:

```bash
sudo ./scripts/deploy.sh /path/to/gpt-go-agent
```

---

## Pengujian dan CI

Baseline lokal:

```bash
go test ./...
go test -race ./...
go vet ./...
go test -cover ./...
go build ./...
```

CI juga menjalankan:

- Staticcheck;
- Gosec;
- Govulncheck.

Aplikasinya sendiri menargetkan Go 1.24 di CI. Govulncheck memakai Go toolchain yang lebih baru untuk rilis vulnerability-scanner saat ini; ini tidak mengubah target build aplikasi.

Cakupan regression keamanan meliputi:

- penolakan parent traversal;
- escape workspace via symlink;
- kebutuhan token mutasi/exec;
- perintah native terbatas;
- autentikasi webhook;
- idempotency webhook;
- output subprocess terbatas;
- recovery backup durable store;
- pembangkitan timestamp audit.

### Smoke test

Jalankan:

```bash
./scripts/smoke.sh
```

Secara default ia memeriksa:

- `/healthz`;
- `/readyz`;
- `/metrics`.

Untuk juga memverifikasi bahwa autentikasi webhook menolak permintaan tanpa autentikasi:

```bash
CHECK_WEBHOOK_AUTH=1 ./scripts/smoke.sh
```

Gunakan opsi itu hanya ketika mode webhook diaktifkan.

---

## Troubleshooting

### Server menolak start: MCP token required

Gejala:

```text
MCP token is required when writes or command execution are enabled
```

Penyebab:

Anda mengaktifkan:

```text
AGENT_ALLOW_WRITE=1
```

atau:

```text
AGENT_ALLOW_COMMAND_EXEC=1
```

tanpa mengonfigurasi `AGENT_MCP_TOKEN`.

Perbaikan:

```bash
export AGENT_MCP_TOKEN="$(openssl rand -hex 32)"
```

Lalu restart service.

---

### Permintaan MCP jarak jauh mengembalikan 401

Periksa:

1. `AGENT_MCP_TOKEN` di-set di daemon;
2. klien mengirim token yang sama;
3. format header persisnya:

```http
Authorization: Bearer ***
```

Periksa juga environment service benar-benar termuat:

```bash
sudo systemctl show gpt-go-agent --property=Environment
sudo journalctl -u gpt-go-agent -n 100
```

Untuk secret yang disimpan lewat `EnvironmentFile`, periksa file itu langsung dengan permission root yang sesuai, bukan mencetak secret ke log bersama.

---

### MCP bekerja lokal tapi tidak jarak jauh

Listener default adalah loopback:

```text
127.0.0.1:8787
```

Ini disengaja.

Lebih memilih secure tunnel/private network daripada mengubah daemon ke bind publik.

Jika Anda sengaja mengubah `AGENT_LISTEN_ADDR`, pastikan:

- autentikasi bearer dikonfigurasi;
- aturan firewall benar;
- TLS/tunneling ada;
- Anda memahami eksposurnya.

Periksa listening socket:

```bash
ss -ltnp | grep 8787
```

---

### `write_file` bilang tulis nonaktif

Aktifkan:

```text
AGENT_ALLOW_WRITE=1
```

dan konfigurasikan:

```text
AGENT_MCP_TOKEN
```

Restart setelah mengubah nilai environment.

Untuk systemd:

```bash
sudo systemctl restart gpt-go-agent
```

---

### Path ditolak padahal kelihatannya di dalam workspace

Server dengan sengaja menolak resolusi path yang keluar lewat `..`, path absolut, atau symlink.

Periksa path:

```bash
readlink -f /var/lib/gpt-go-agent/workspace/path/to/item
```

Jika resolve ke luar `AGENT_WORKSPACE`, penolakan itu memang seharusnya.

Pindahkan/salin data yang dibutuhkan ke dalam workspace, bukan melemahkan batas root.

---

### `exec_command` bilang perintah tidak ada di allowlist

Tambahkan nama executable ke:

```text
AGENT_ALLOWED_COMMANDS
```

tapi ingat bahwa ini hanya gerbang satu.

Contoh:

```text
AGENT_ALLOWED_COMMANDS=uptime,pwd,date
```

Restart service.

---

### Perintah di-allowlist tapi tetap bilang tidak diizinkan

Artinya kebijakan terbatas deterministik menolaknya.

Ini memang seharusnya untuk primitif eksekusi umum seperti:

```text
python3
node
bash
git
go
make
curl
```

Jangan memperlakukan `AGENT_ALLOWED_COMMANDS` sebagai cara melewati kebijakan.

Jika kapabilitas native baru benar-benar dibutuhkan, implementasikan validator sempit untuk perintah/subcommand itu di kebijakan deterministik dan tambahkan test.

---

### `cwd` kustom ditolak

Eksekusi native MCP terbatas dengan sengaja berjalan dari root workspace.

Gunakan operasi relatif-workspace lewat tool file.

Jika perintah masa depan butuh subdirektori, tambahkan operasi server-side dengan validasi sempit daripada membuka kembali penanganan path `cwd` sembarang.

---

### `/webhook` mengembalikan 404

Mode webhook kemungkinan nonaktif.

Set:

```text
AGENT_WEBHOOK_ENABLED=1
AGENT_WEBHOOK_TOKEN=<secret>
```

dan restart.

Saat mode webhook nonaktif, route webhook dengan sengaja tidak di-mount.

---

### Webhook mengembalikan 401

Verifikasi:

```text
AGENT_WEBHOOK_TOKEN
```

dan kirim:

```http
Authorization: Bearer YOUR_W...OKEN
```

Token MCP dan token webhook adalah credential terpisah.

---

### Webhook POST mengembalikan 409

Anda memakai ulang `Idempotency-Key` dengan konten intent/context berbeda.

Gunakan salah satu:

- body permintaan asli untuk key tersebut; atau
- idempotency key baru.

---

### Webhook mengembalikan 429

Request limiter sudah terlewati.

Hormati header `Retry-After` dan retry nanti.

Jika traffic berkelanjutan diharapkan, evaluasi dulu kapasitas worker dan skalabilitas storage daripada sekadar menaikkan request limit.

---

### Webhook mengembalikan 503 atau job gagal selama evaluasi Middleman

Periksa:

```text
AGENT_MIDDLEMAN_URL
AGENT_MIDDLEMAN_KEY
AGENT_MIDDLEMAN_MODEL
```

Verifikasi konektivitas dari host service.

Periksa log:

```bash
journalctl -u gpt-go-agent -n 200
```

Periksa juga `/metrics` untuk informasi retry/circuit Middleman saat tersedia.

---

### Job Codex gagal: executable not found

Periksa:

```text
AGENT_CODEX_BIN
```

Verifikasi dari environment service:

```bash
command -v codex
```

Untuk deployment systemd, ingat bahwa `PATH` service dapat berbeda dari shell interaktif Anda.

Gunakan path executable absolut eksplisit di `AGENT_CODEX_BIN` saat diperlukan.

---

### Codex bisa baca tapi tidak bisa modifikasi file

Itu default-nya.

Set:

```text
AGENT_CODEX_ALLOW_WRITE=1
```

hanya jika mutasi workspace memang sengaja dibutuhkan.

Restart setelah itu.

---

### Job store gagal load

Lokasi default:

```text
/var/lib/gpt-go-agent/jobs/store.json
```

Periksa:

```bash
ls -la /var/lib/gpt-go-agent/jobs/
```

Service account harus bisa menulis ke direktori.

Jika primary corrupt/hilang, loader otomatis coba:

```text
store.json.bak
```

Jika keduanya ada tapi invalid, startup gagal dengan sengaja agar state rusak tidak dibuang diam-diam.

---

### Permission denied di bawah systemd

Service berjalan sebagai:

```text
gptagent:gptagent
```

dan systemd hanya mengizinkan tulis di bawah:

```text
/var/lib/gpt-go-agent
```

Periksa ownership:

```bash
sudo chown -R gptagent:gptagent /var/lib/gpt-go-agent
sudo chmod 0700 /var/lib/gpt-go-agent
```

Jangan melonggarkan `ProtectSystem` atau permission filesystem secara luas hanya untuk membuat satu path jalan. Lebih baik pindahkan state agent yang writable ke direktori service yang dimaksud.

---

### `/readyz` mengembalikan 503

Dalam mode MCP-only seharusnya normal ready setelah startup.

Dalam mode webhook, verifikasi konfigurasi URL/model Middleman.

Periksa:

```bash
curl -i http://127.0.0.1:8787/readyz
journalctl -u gpt-go-agent -n 100
```

---

### `/metrics` kosong

Ini normal dalam mode MCP-only.

Worker metrics diregistrasi hanya ketika mode webhook diaktifkan.

---

### Audit log tidak ada

Periksa:

```text
AGENT_AUDIT_PATH
```

dan permission direktori parent.

Contoh produksi:

```text
AGENT_AUDIT_PATH=/var/lib/gpt-go-agent/agent-audit.jsonl
```

Service account harus bisa membuat/menulis file.

---

### Deployment script rollback

`scripts/deploy.sh` rollback jika:

- systemd gagal menjadi active;
- `/healthz` gagal;
- `/readyz` gagal.

Periksa:

```bash
systemctl status gpt-go-agent
journalctl -u gpt-go-agent -n 200
```

Perbaiki readiness/konfigurasi sebelum retry deployment.

---

## Rekomendasi operasional

Untuk deployment privat kecil:

1. pertahankan `AGENT_LISTEN_ADDR=127.0.0.1:8787`;
2. gunakan secure tunnel/private network untuk akses MCP jarak jauh;
3. konfigurasikan token MCP random panjang sebelum mengaktifkan mutasi atau eksekusi;
4. pertahankan `AGENT_ALLOW_WRITE=0` kecuali dibutuhkan;
5. pertahankan `AGENT_ALLOW_COMMAND_EXEC=0` kecuali dibutuhkan;
6. gunakan allowlist native minimal yang dibutuhkan;
7. pertahankan mode webhook nonaktif kecuali benar-benar dipakai;
8. gunakan token webhook terpisah dari token MCP;
9. pertahankan Codex read-only kecuali job coding harus memodifikasi file;
10. pertahankan workspace dengan scope sempit;
11. periksa audit log;
12. monitor `/readyz`, queue depth, kegagalan, dan penggunaan disk;
13. backup state job/audit yang Anda pedulikan.

Untuk eksposur produksi, juga tambahkan:

- TLS atau secure tunnel tepercaya;
- aturan firewall;
- rotasi secret;
- retensi log;
- monitoring/alerts;
- update dependency dan OS berkala.

---

## Tindak lanjut arsitektur yang diketahui

### Job persistence

Webhook store saat ini adalah durable JSON snapshot store. Ia menulis ulang peta job yang dipertahankan pada pembaruan persistensi.

Ini sengaja sederhana dan ringan secara operasional, tapi bukan backend jangka panjang yang tepat untuk traffic job volume tinggi.

Arah masa depan yang direkomendasikan:

```text
SQLite
+ WAL
+ indexed pagination
+ UNIQUE idempotency keys
+ transactional state transitions
+ efficient retention deletes
```

### Implementasi protokol MCP

Lapisan transport/protok ol MCP saat ini ditulis tangan dan dipin ke:

```text
2025-06-18
```

Perubahan masa depan yang fokus-kompatibel sebaiknya migrasikan lapisan transport/protok ol ke MCP Go SDK resmi dengan tes kompatibilitas klien eksplisit.

Migrasi itu sebaiknya tetap terpisah dari perubahan kebijakan eksekusi agar perilaku protokol dan perilaku keamanan dapat di-review secara independen.

### Kapabilitas eksekusi

Permukaan perintah terbatas sengaja kecil.

Jika kapabilitas tambahan dibutuhkan, lebih memilih menambah **tool bertipe sempit atau subcommand dengan validasi sempit** daripada memperluas ke arah eksekusi proses sembarang.

---

## Lisensi / kepemilikan

Repositori ini saat ini dikelola sebagai proyek privat. Tambahkan lisensi eksplisit sebelum mendistribusikannya secara publik.

---

## Tentang terjemahan ini

Versi Bahasa Indonesia ini adalah terjemahan manual dari README asli (`README.en.md` atau default branch).

- **Tujuan**: membantu deployment dan audit oleh operator Indonesia yang lebih nyaman membaca dokumentasi dalam Bahasa Indonesia.
- **Konsistensi**: istilah teknis (nama env var, nama executable, nama endpoint, nama modul Go, nama protokol) tetap memakai bentuk aslinya dalam English. Bagian naratif, instruksi, dan rekomendasi diterjemahkan utuh.
- **Sinkronisasi**: terjemahan diperbarui ketika README sumber berubah. Karena README sumber ditulis tangan dalam English, terjemahan ini bukan hasil terjemahan otomatis, jadi beberapa kalimat mungkin tertinggal setelah perubahan upstream.
- **Kontributor**: kalau Anda menemukan ketidaksesuaian antara README Bahasa Indonesia dan perilakunya, mohon perbaiki dari README sumber (English) terlebih dahulu, kemudian perbarui terjemahan.
