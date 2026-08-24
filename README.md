# relay-bridge

Bridge HTTP/SOCKS5 proxy lokal yang meneruskan trafik HTTP **melalui satu atau banyak relay HTTP serverless** (Vercel / Cloudflare Workers). Mendukung **multi-relay dengan round-robin**, dipisah per **tipe** (`vercel` / `cloudflare`) lewat environment variable — kedua tipe **tidak pernah dicampur** dalam satu proses.

Relay itu sendiri adalah *catch-all HTTP relay*: kamu kirim request ke sana dengan header kontrol `x-relay-target` (destinasi `scheme://host`), opsional `x-relay-path` (path+query destinasi), lalu relay men-fetch URL itu **di sisi server** dan mengembalikan responsnya. `relay-bridge` menerjemahkan protokol proxy standar (HTTP forward proxy & SOCKS5) ke protokol header relay tersebut.

## Fitur

| Fitur | Status |
|-------|--------|
| HTTP forward proxy (absolute-form `http://` & `https://`) | ✅ (hanya **GET** & **HEAD** — lihat batas) |
| SOCKS5 CONNECT ke target **HTTP** (port 80) | ✅ (GET/HEAD) |
| CONNECT (HTTP proxy) ke port 80 | ✅ (GET/HEAD) |
| **Multi-relay** (>1) → round-robin | ✅ |
| **Pemisahan tipe** `vercel` / `cloudflare` via env | ✅ (tidak pernah dicampur) |
| Failover ke relay berikutnya saat transport error | ✅ |
| Streaming / SSE / chunked | ✅ (passthrough, `x-accel-buffering: no`) |
| Health / readiness (`/`, `/health`) | ✅ (JSON `{"status":"ok"}`) |
| HTTPS browsing (CONNECT ke 443) | `-http` + `-mitm` ✅ |
| HTTPS tanpa `-mitm` | ❌ (502; relay HTTP-only) |

> **Batas method penting (GET/HEAD only):** relay serverless ini **hanya merutekan `GET` dan `HEAD`** — POST/PUT/PATCH/DELETE dibalas relay dengan `405 Method Not Allowed` (header `allow: HEAD, GET, OPTIONS`). Karena itu relay-bridge **menolak method non-GET/HEAD di proxy dengan `405`** dan pesan yang jelas, alih-alih meneruskan ke relay. Intinya bridge ini cocok untuk **browse/ambil konten** (halaman, aset, API GET) — **tidak** untuk submit form / API POST / upload. Ini batasan relay, bukan bug proxy.

> **Untuk browser:** relay ini dipakai untuk situs **http://** (via `-http`/`-socks`) dan **https://** (via `-http` + `-mitm`, setelah CA di-trust di browser/OS). Tanpa `-mitm`, HTTPS tidak termuat karena browser membuat tunnel `CONNECT ...:443`, sedangkan relay HTTP-only tidak bisa membawa tunnel TCP mentah. Tes nyata: `curl -x http://127.0.0.1:8080 http://example.com/` → 200 HTML; `curl -x http://127.0.0.1:8080 --cacert <ca> https://example.com` → 200 HTML.

## Konfigurasi relay (env)

Relay dikonfigurasi lewat environment. Ada **dua tipe**, dan satu proses memakai **satu tipe saja** — tidak dicampur.

| Env | Keterangan |
|-----|-----------|
| `RELAY_TYPE` | `vercel` \| `cloudflare` (juga menerima `relay vercel` / `relay cloudflare`). **Yang memilih tipe.** |
| `RELAY_VERCEL` | daftar relay Vercel, dipisah koma. Dipakai **hanya** jika `RELAY_TYPE=vercel`. |
| `RELAY_CLOUDFLARE` | daftar relay Cloudflare Workers, dipisah koma. Dipakai **hanya** jika `RELAY_TYPE=cloudflare`. |
| `RELAY_SECRET` | opsional; dikirim sebagai `x-relay-secret`. |
| `RELAY_URL` | mode single-legacy (dipakai jika `RELAY_TYPE` kosong). |

> **Bisa via file `.env`:** binary juga auto-load `./.env` (dotenv) di direktori kerja saat start. Jadi kamu bisa isi `RELAY_TYPE` + `RELAY_VERCEL`/`RELAY_CLOUDFLARE` di `.env` (contoh di `.env.example`) dan langsung jalankan `./relay-bridge` tanpa export. **Env yang sudah diset (mis. oleh pm2) TIDAK ditimpa** oleh `.env` — pm2/ecosystem.config.js tetap menang.

> **Aturan "tidak dicampur":** walau kedua daftar bisa dideklarasikan, hanya daftar yang cocok dengan `RELAY_TYPE` yang dimuat ke pool. Satu proses tidak akan pernah mencampur vercel & cloudflare. `RELAY_TYPE` yang tidak valid → exit 2 dengan pesan jelas.

## Cara kerja

Klien → `relay-bridge` (localhost) → relay terpilih (round-robin) → origin.

```
Request klien                        Header yang dikirim ke relay
------------------                  ------------------------------------------
method /body /headers               method /body /headers yang sama (minus hop-by-hop)
x-relay-target: <scheme>://<host>   destinasi
x-relay-path:   /path?query         path + query destinasi
x-relay-secret: <secret>            (opsional, hanya kalau dikonfigurasi)
```

**Round-robin & failover:** `RelayPool` menyimpan daftar relay dari satu tipe. Setiap request memakai relay berikutnya secara bergiliran; jika satu relay gagal karena transport error (DNS/connect/timeout), pool mencoba relay berikutnya. HTTP error (4xx/5xx) dari origin **bukan** kegagalan — itu respons sah dan diteruskan apa adanya.

## Pasang

Prasyarat: Go 1.22+ (dibuat & diuji dengan 1.23.6).

```bash
go build -o relay-bridge .
```

## Pemakaian

### Mode pool (multi-relay, via env)

```bash
export RELAY_TYPE=vercel
export RELAY_VERCEL="https://relay-deployer-santo-...,https://relay-deployer-auah-fp850hnlh-...,https://relay-deployer-sengkyou-..."
export RELAY_CLOUDFLARE="https://aurora8456-...,https://caleb05-...,https://salma31-..."

./relay-bridge -http 127.0.0.1:8080 -socks 127.0.0.1:1080
# verbose — log relay mana yang dipakai tiap request:
./relay-bridge -http 127.0.0.1:8080 -socks 127.0.0.1:1080 -v
```

### Mode single (flag `-relay` atau `RELAY_URL`)

```bash
./relay-bridge -relay https://relay-deployer-auah2-ar88po5k4-auah-s-projects.vercel.app \
  -http 127.0.0.1:8080 -socks 127.0.0.1:1080
```

### Pakai sebagai proxy

```bash
# HTTP proxy (GET/HEAD)
curl -x http://127.0.0.1:8080 http://httpbin.org/get

# SOCKS5 (GET/HEAD, target HTTP)
curl --socks5-hostname 127.0.0.1:1080 http://httpbin.org/get

# HTTPS via browser (MITM)
curl -x http://127.0.0.1:8080 --cacert ~/.relay-bridge/mitm-ca.crt https://example.com

# POST tidak bisa (relay GET/HEAD only) -> 405
curl -x http://127.0.0.1:8080 -X POST http://httpbin.org/post -d '{"a":1}'   # -> 405
```

## pm2

Gunakan `ecosystem.config.js` (sudah berisi daftar relay + pemilihan tipe). Ganti tipe dengan mengubah `RELAY_TYPE`, lalu restart.

```bash
pm2 start ecosystem.config.js
pm2 logs relay-bridge          # lihat relay mana yang dipakai (pakai -v)
pm2 restart relay-bridge --update-env   # setelah ganti RELAY_TYPE
```

Opsi jalur (address listen, MITM, verbose) di-set lewat `args` di `ecosystem.config.js`; daftar relay + tipe di-set lewat `env`.

## Mode MITM (HTTPS untuk browser)

Relay di sini HTTP-only (serverless), jadi `CONNECT :443` tidak bisa di-tunnel langsung. Mode `-mitm` menyelesaikannya dengan **mengakhiri TLS di sisi proxy** (MITM) lalu meneruskan request yang sudah didekripsi ke relay sebagai URL https absolute-form — relay men-fetch TLS-nya sendiri di server. Artinya proxy jadi titik TLS-intercept: semua HTTPS terlihat terdekripsi di proxy, dan hanya aman jika kamu yang mengoperasikan & menyimpan CA-nya.

**Langkah:**

1. Jalankan dengan `-mitm`:
   ```bash
   ./relay-bridge -http 127.0.0.1:8080 -socks "" -mitm   # pakai RELAY_TYPE + list dari env
   # atau mode single:
   ./relay-bridge -relay https://relay-...vercel.app -http 127.0.0.1:8080 -mitm
   ```
   Pada run pertama, CA dibuat di `~/.relay-bridge/mitm-ca.crt` (override dengan `-certs DIR`). Log menampilkan path + SHA-256 fingerprint CA.

2. **Trust CA di browser/OS** yang akan browsing:
   - Chrome/Edge (Windows/Linux): Settings → Privacy & security → Security → Manage certificates → Trusted Root Certification Authorities → Import `mitm-ca.crt`.
   - Firefox: Settings → Privacy & Security → Certificates → View Certificates → Authorities → Import.
   - macOS: Keychain Access → System Roots → import, lalu set "Always Trust".
   - `curl`: pakai `--cacert ~/.relay-bridge/mitm-ca.crt`.

3. Set proxy di browser/klien ke `http://127.0.0.1:8080` (atau host:port tempat bridge berjalan). Semua situs http & https kini mengalir lewat relay.

> **Catatan CA:** CA disimpan di `-certs` (default `~/.relay-bridge`) dan **stabil** antar restart (jangan dihapus, atau browser gagal memverifikasi). Fingerprint ditampilkan di log startup — verifikasi sebelum mempercayai.

### Flags

| Flag | Default | Keterangan |
|------|---------|-----------|
| `-relay` | `""` (juga baca `$RELAY_URL`) | URL relay tunggal (`scheme://host`); **menimpa** semua config pool |
| `-secret` | `""` (baca `$RELAY_SECRET`) | nilai `x-relay-secret` yang dikirim ke relay |
| `-http` | `127.0.0.1:8080` | listen HTTP proxy; `""` untuk mematikan |
| `-socks` | `127.0.0.1:1080` | listen SOCKS5; `""` untuk mematikan |
| `-mitm` | `false` | aktifkan TLS-intercept HTTPS (perlu CA di-trust di klien) |
| `-certs` | `~/.relay-bridge` | direktori CA MITM + cache leaf |
| `-v` | `false` | verbose: log tiap request + relay yang dipakai |

> `-relay` kini opsional — bila tidak ada, pool dibangun dari `RELAY_TYPE` + list yang sesuai. Bila `-relay`/`RELAY_URL`/`RELAY_TYPE` semuanya kosong → exit 2 dengan pesan jelas.

## Verifikasi

```bash
go vet ./... && go build -o relay-bridge .
hermes verify --json   # build -> test -> boot -> readiness poll (health /)
```

HTTP proxy mengekspos endpoint health `GET /` dan `GET /health` yang membalas `{"status":"ok"}` — dipakai untuk readiness poll verifier/monitor.

## Pitfalls

- **GET/HEAD only** — POST/PUT/PATCH/DELETE ditolak dengan `405` oleh relay serverless; proxy menolaknya di depan dengan pesan jelas (`Allow: GET, HEAD`). Batasan relay, bukan bug proxy.
- **`x-relay-target` wajib `scheme://host` saja.** Jangan masukkan path di situ — pakai `x-relay-path`. Host-only (`httpbin.org`) ditolak relay.
- **`x-relay-path` mengoverride path target.** Kalau kosong, relay memakai path yang melekat di `x-relay-target`.
- **Tipe tidak boleh dicampur** — `RELAY_TYPE` memilih satu daftar; dua tipe tidak pernah aktif bersamaan. `RELAY_TYPE` tak valid → exit 2.
- **Respons di-re-chunked oleh Go server** bila origin tidak mengirim `Content-Length` (mis. streaming/SSE). Normal; klien HTTP men-decode chunked secara transparan.
- **MITM TLS tanpa proteksi** — sengaja; hanya aman jika kamu pemilik & penyimpan CA.
- Header hop-by-hop (`Connection`, `Transfer-Encoding`, dll.) dan header kontrol `x-relay-*` disaring — tidak bocor ke origin. `x-relay-secret` dikirim hanya jika dikonfigurasi.

## Lisensi

MIT
