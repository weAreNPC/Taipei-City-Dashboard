
"""
Fast GeoJSON generator for ntpc_green_stores — fetches API pages then
geocodes concurrently via ThreadPoolExecutor.

Run from project root (no Docker needed — output goes to local FE public dir):
    pip install requests
    python script/generate_ntpc_geojson_fast.py
"""

import json
import os
import re
import sys
import time
from concurrent.futures import ThreadPoolExecutor, as_completed
from io import StringIO

import requests
import pandas as pd

API_URL = "https://data.ntpc.gov.tw/api/datasets/6ccd0274-0c09-43b0-98fc-4d5222a71e8b/csv"
ARCGIS_URL = "https://geocode.arcgis.com/arcgis/rest/services/World/GeocodeServer/findAddressCandidates"

OUTPUT_PATH = os.path.join(
    os.path.dirname(__file__),
    "..", "Taipei-City-Dashboard-FE", "public", "mapData", "ntpc_green_stores.geojson"
)

MAX_WORKERS = 10


def fetch_all_pages():
    frames, page = [], 0
    while True:
        resp = requests.get(API_URL, params={"page": page},
                            headers={"accept": "text/csv;charset=UTF-8"}, timeout=30)
        resp.raise_for_status()
        body = resp.text.strip()
        if not body:
            break
        df = pd.read_csv(StringIO(body))
        if df.empty:
            break
        frames.append(df)
        page += 1
        if len(df) < 30:
            break
    return pd.concat(frames, ignore_index=True)


def geocode(address):
    clean = re.sub(r"^\d{3}", "", str(address)).strip()
    try:
        r = requests.get(ARCGIS_URL, params={
            "SingleLine": clean,
            "f": "json",
            "outSR": '{"wkid":4326}',
            "outFields": "Addr_type,Match_addr",
            "maxLocations": 1,
        }, timeout=15)
        r.raise_for_status()
        candidates = r.json().get("candidates", [])
        if candidates:
            loc = candidates[0]["location"]
            return loc["x"], loc["y"]
    except Exception:
        pass
    return None, None


def main():
    print("Fetching API pages...")
    df = fetch_all_pages()
    total = len(df)
    print(f"  {total} rows fetched")

    print(f"Geocoding with {MAX_WORKERS} workers...")
    lngs = [None] * total
    lats = [None] * total
    done = 0

    with ThreadPoolExecutor(max_workers=MAX_WORKERS) as pool:
        futures = {pool.submit(geocode, addr): i for i, addr in enumerate(df["address"])}
        for fut in as_completed(futures):
            idx = futures[fut]
            lng, lat = fut.result()
            lngs[idx] = lng
            lats[idx] = lat
            done += 1
            if done % 100 == 0:
                print(f"  {done}/{total}")

    df["lng"] = lngs
    df["lat"] = lats
    geocoded = sum(1 for x in lngs if x is not None)
    print(f"  Geocoded: {geocoded}/{total}")

    features = []
    for _, row in df.iterrows():
        import math
        if row["lng"] is None or row["lat"] is None:
            continue
        if isinstance(row["lng"], float) and math.isnan(row["lng"]):
            continue
        features.append({
            "type": "Feature",
            "properties": {
                "name":             row["name"],
                "type":             row["type"],
                "address":          row["address"],
                "number":           row["number"],
                "localcallservice": row["localcallservice"],
            },
            "geometry": {
                "type": "Point",
                "coordinates": [row["lng"], row["lat"]],
            },
        })

    geojson = {
        "type": "FeatureCollection",
        "crs": {"type": "name", "properties": {"name": "urn:ogc:def:crs:OGC:1.3:CRS84"}},
        "features": features,
    }

    out = os.path.normpath(OUTPUT_PATH)
    os.makedirs(os.path.dirname(out), exist_ok=True)
    with open(out, "w", encoding="utf-8") as f:
        json.dump(geojson, f, ensure_ascii=False, separators=(",", ":"))
    print(f"\nSaved {len(features)} features → {out}")


if __name__ == "__main__":
    main()
