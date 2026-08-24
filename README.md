# relay-bridge

Bridge HTTP/SOCKS5 proxy lokal yang meneruskan seluruh trafik HTTP **melalui relay HTTP serverless** (mis. `https://relay-deployer-auah2-ar88po5k4-auah-s-projects.vercel.app`).

Relay itu sendiri adalah *catch-all HTTP relay*: kamu kirim request apa pun ke sana dengan header kontrol `x-relay-target` (destinasi `scheme://host`), opsional `x-relay-path` (path+query destinasi), lalu relay men-fetch URL itu **di sisi server** dan mengembalikan responsnya. `relay-bridge` menerjemahkan protokol proxy standar (HTTP forward proxy & SOCKS5) ke protokol header relay tersebut.

## Fitur

| Mode | Protokol | Status |
|------|----------|--------|
| HTTP forward proxy (absolute-form `http://` & `https://`) | `-http` | ✅ penuh |
| SOCKS5 CONNECT ke target **HTTP** (port 80) | `-socks` | ✅ |
| CONNECT (HTTP proxy) ke port 80 | `-http` | ✅ |
| Streaming / SSE / chunked | — | ✅ (passthrough, `x-accel-buffering: no`) |
| Health / readiness (`/`, `/health`) | `-http` | ✅ (JSON `{"status":"ok"}`) |
| HTTPS browsing biasa (CONNECT ke 443) | — | ❌ tidak didukung (relay HTTP-only, tanpa tunnel TCP mentah) |

> **Untuk browser:** relay ini HANYA bisa dipakai untuk situs **http://** (baik via `-http` maupun `-socks`). Situs **https://** TIDAK akan termuat di browser karena browser selalu membuat tunnel `CONNECT ...:443` untuk HTTPS, dan relay HTTP-only tidak bisa membawa tunnel TCP mentah (proxy balas 502). Tes nyata: `curl -x http://127.0.0.1:8080 http://example.com/` → 200 HTML asli; `curl -x http://127.0.0.1:8080 https://example.com` → `CONNECT tunnel failed, response 502`.

> **Batas teknis utama:** relay berjalan sebagai fungsi serverless Vercel yang **hanya HTTP** (request/response) — ia tidak bisa membawa tunnel TCP mentah. Karena itu:
> - `CONNECT`/SOCKS5 ke **port 443 (HTTPS)** dan protokol non-HTTP tidak bisa di-tunnel: proxy menolak dengan bersih (502 / SOCKS5 `command not supported`). Tidak ada MITM TLS yang dibangun di sini.
> - HTTPS tetap bisa diakses lewat **absolute-form** (`GET https://host/path HTTP/1.1` ke proxy) karena relay men-fetch TLS-nya sendiri di server.

## Cara kerja

Klien → `relay-bridge` (localhost) → relay serverless → origin. `relay-bridge` memetakan:

```
Request klien                          Header yang dikirim ke relay
-------------------------              ------------------------------------------
method /body /headers                  method /body /headers yang sama (minus hop-by-hop)
x-relay-target: <scheme>://<host>     destinasi
x-relay-path:   /path?query           path + query destinasi
x-relay-secret: <secret>              (opsional, hanya kalau dikonfigurasi)
```

Respon origin dikembalikan ke klien apa adanya (status, header, body stream).

## Pasang

Prasyarat: Go 1.22+ (dibuat & diuji dengan 1.23.6).

```bash
go build -o relay-bridge .
```

## Pemakaian

```bash
# relay wajib; default listen HTTP :8080 & SOCKS5 :1080
./relay-bridge \
  -relay https://relay-deployer-auah2-ar88po5k4-auah-s-projects.vercel.app \
  -http 127.0.0.1:8080 \
  -socks 127.0.0.1:1080
```

Pakai sebagai proxy:

```bash
# HTTP proxy (http & https absolute-form berfungsi)
curl -x http://127.0.0.1:8080 http://httpbin.org/get
curl -x http://127.0.0.1:8080 -X POST http://httpbin.org/post -d '{"a":1}'

# SOCKS5 (target HTTP)
curl --socks5-hostname 127.0.0.1:1080 http://httpbin.org/get
```

### Flags

| Flag | Default | Keterangan |
|------|---------|-----------|
| `-relay` | (wajib, juga baca `$RELAY_URL`) | URL dasar relay (`scheme://host`) |
| `-secret` | `""` (baca `$RELAY_SECRET`) | nilai `x-relay-secret` yang dikirim ke relay |
| `-http` | `127.0.0.1:8080` | listen HTTP proxy; `""` untuk mematikan |
| `-socks` | `127.0.0.1:1080` | listen SOCKS5; `""` untuk mematikan |

## Verifikasi cepat

```bash
./relay-bridge -relay https://relay-deployer-auah2-ar88po5k4-auah-s-projects.vercel.app \
  -http 127.0.0.1:8080 -socks 127.0.0.1:1080 &

curl -sS -x http://127.0.0.1:8080 http://httpbin.org/get            # -> JSON (http)
curl -sS --socks5-hostname 127.0.0.1:1080 http://httpbin.org/get   # -> JSON (socks, http)
curl -sS -x http://127.0.0.1:8080 https://httpbin.org/get          # -> 502 CONNECT not supported (limitation)
```

## Verifikasi

```bash
go vet ./... && go build -o relay-bridge .
hermes verify --json   # build -> test -> boot -> readiness poll (health /)
```

HTTP proxy mengekspos endpoint health `GET /` dan `GET /health` yang membalas `{"status":"ok"}` — dipakai untuk readiness poll verifier/monitor.

## Pitfalls

- **`x-relay-target` wajib full URL dengan scheme.** Host-only (`httpbin.org`) ditolak relay dengan `Invalid x-relay-target URL`.
- **`x-relay-path` mengoverride path target.** Kalau kosong, relay memakai path yang melekat di `x-relay-target`. Path + query request relay sendirian *tidak* dipakai.
- **Respons di-re-chunked oleh Go server** bila origin tidak mengirim `Content-Length` (mis. streaming/SSE). Ini normal; klien HTTP men-decode chunked secara transparan.
- **Tidak ada pengamanan MITM TLS** — sengaja. Untuk HTTPS mutlak via bridge, gunakan absolute-form https (bukan CONNECT).
- Header hop-by-hop (`Connection`, `Transfer-Encoding`, dll.) dan header kontrol `x-relay-*` disaring — tidak bocor ke origin. `x-relay-secret` dikirim hanya jika dikonfigurasi.

## Lisensi

MIT
