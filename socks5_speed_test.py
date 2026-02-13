#!/usr/bin/env python3
# -*- coding: utf-8 -*-

import argparse
import statistics
import time

import requests


def mbps(byte_count: float, seconds: float) -> float:
    if seconds <= 0:
        return 0.0
    return (byte_count * 8) / seconds / 1_000_000


def mibs(byte_count: float, seconds: float) -> float:
    if seconds <= 0:
        return 0.0
    return byte_count / seconds / (1024 * 1024)


def build_proxy_url(
    host: str, port: int, username: str = "", password: str = ""
) -> str:
    if username:
        return f"socks5h://{username}:{password}@{host}:{port}"
    return f"socks5h://{host}:{port}"


def get_session(proxy_url: str) -> requests.Session:
    session = requests.Session()
    session.trust_env = False
    session.proxies = {"http": proxy_url, "https": proxy_url}
    return session


def test_egress(session: requests.Session, timeout: float):
    ip_resp = session.get("https://api.ipify.org?format=json", timeout=timeout)
    ip_resp.raise_for_status()
    ip = ip_resp.json().get("ip", "unknown")

    geo = {}
    try:
        geo_resp = session.get("https://ipapi.co/json/", timeout=timeout)
        geo_resp.raise_for_status()
        geo = geo_resp.json()
    except Exception:
        pass
    return ip, geo


def download_speed_test(session: requests.Session, total_bytes: int, timeout: float):
    url = f"https://speed.cloudflare.com/__down?bytes={total_bytes}"
    start = time.perf_counter()
    downloaded = 0
    with session.get(url, stream=True, timeout=timeout) as response:
        response.raise_for_status()
        for chunk in response.iter_content(chunk_size=64 * 1024):
            if chunk:
                downloaded += len(chunk)
    elapsed = time.perf_counter() - start
    return downloaded, elapsed, mbps(downloaded, elapsed), mibs(downloaded, elapsed)


def upload_speed_test(session: requests.Session, total_bytes: int, timeout: float):
    url = "https://speed.cloudflare.com/__up"
    chunk = b"0" * (64 * 1024)

    def generator(size: int):
        sent = 0
        while sent < size:
            remaining = size - sent
            if remaining >= len(chunk):
                yield chunk
                sent += len(chunk)
            else:
                yield b"0" * remaining
                sent += remaining

    start = time.perf_counter()
    response = session.post(
        url,
        data=generator(total_bytes),
        headers={"Content-Type": "application/octet-stream"},
        timeout=timeout,
    )
    response.raise_for_status()
    elapsed = time.perf_counter() - start
    return total_bytes, elapsed, mbps(total_bytes, elapsed), mibs(total_bytes, elapsed)


def fmt_bytes(n: int) -> str:
    units = ["B", "KB", "MB", "GB", "TB"]
    value = float(n)
    idx = 0
    while value >= 1024 and idx < len(units) - 1:
        value /= 1024
        idx += 1
    return f"{value:.2f} {units[idx]}"


def main():
    parser = argparse.ArgumentParser(description="SOCKS5 speed test (egress/down/up)")
    parser.add_argument("--host", required=True)
    parser.add_argument("--port", required=True, type=int)
    parser.add_argument("--user", default="")
    parser.add_argument("--password", default="")
    parser.add_argument("--download-bytes", type=int, default=30 * 1024 * 1024)
    parser.add_argument("--upload-bytes", type=int, default=20 * 1024 * 1024)
    parser.add_argument("--rounds", type=int, default=3)
    parser.add_argument("--timeout", type=float, default=20.0)
    args = parser.parse_args()

    proxy_url = build_proxy_url(args.host, args.port, args.user, args.password)
    session = get_session(proxy_url)

    print("== SOCKS5 speed test ==")
    print(f"Proxy: {proxy_url}")

    print("\n[1/3] Egress test...")
    try:
        ip, geo = test_egress(session, args.timeout)
        print(f"Egress IP: {ip}")
        if geo:
            print(
                "Location: "
                f"{geo.get('country_name', 'unknown')} / {geo.get('city', 'unknown')}"
            )
            print(f"Org: {geo.get('org', 'unknown')}")
            print(f"ASN: {geo.get('asn', 'unknown')}")
    except Exception as exc:
        print(f"Egress test failed: {exc}")
        return 1

    print("\n[2/3] Download test...")
    down_results = []
    for i in range(1, args.rounds + 1):
        try:
            d_bytes, d_sec, d_mbps, d_mibs = download_speed_test(
                session, args.download_bytes, args.timeout
            )
            down_results.append(d_mbps)
            print(
                f"Round {i}: {fmt_bytes(d_bytes)} / {d_sec:.2f}s "
                f"-> {d_mbps:.2f} Mbps ({d_mibs:.2f} MiB/s)"
            )
        except Exception as exc:
            print(f"Round {i}: download failed -> {exc}")

    print("\n[3/3] Upload test...")
    up_results = []
    for i in range(1, args.rounds + 1):
        try:
            u_bytes, u_sec, u_mbps, u_mibs = upload_speed_test(
                session, args.upload_bytes, args.timeout
            )
            up_results.append(u_mbps)
            print(
                f"Round {i}: {fmt_bytes(u_bytes)} / {u_sec:.2f}s "
                f"-> {u_mbps:.2f} Mbps ({u_mibs:.2f} MiB/s)"
            )
        except Exception as exc:
            print(f"Round {i}: upload failed -> {exc}")

    print("\n== Results ==")
    if down_results:
        print(f"Download avg: {statistics.mean(down_results):.2f} Mbps")
        print(f"Download peak: {max(down_results):.2f} Mbps")
    else:
        print("Download avg: N/A")

    if up_results:
        print(f"Upload avg: {statistics.mean(up_results):.2f} Mbps")
        print(f"Upload peak: {max(up_results):.2f} Mbps")
    else:
        print("Upload avg: N/A")

    return 0


if __name__ == "__main__":
    raise SystemExit(main())
