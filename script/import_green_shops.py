"""
import_green_shops.py

Read geocoded Taipei / New Taipei green shop CSV files, insert into
PostgreSQL dashboard DB, and regenerate the three GeoJSON map files.

Usage (local, with postgres port exposed):
    DB_DASHBOARD_HOST=localhost DB_DASHBOARD_PORT=5433 python script/import_green_shops.py

Usage (inside Docker network):
    docker run --rm --network br_dashboard \\
        -v "$(pwd):/repo" -w /repo \\
        -e DB_DASHBOARD_HOST=postgres-data \\
        python:3.11-slim bash -c \\
        "pip install -q pandas sqlalchemy psycopg2-binary && python script/import_green_shops.py"
"""

import json
import math
import os
import sys
from pathlib import Path

import numpy as np
import pandas as pd

try:
    from sqlalchemy import create_engine, text
    _HAS_SQLALCHEMY = True
except ImportError:
    _HAS_SQLALCHEMY = False

SCRIPT_DIR = Path(__file__).parent
ROOT_DIR = SCRIPT_DIR.parent
FE_MAP_DATA = ROOT_DIR / "Taipei-City-Dashboard-FE" / "public" / "mapData"

TAIPEI_CSV = ROOT_DIR / "taipei_green_shop_coordinates.csv"
NTPC_CSV   = ROOT_DIR / "new_taipei_green_shop_coordinates.csv"

TAIPEI_GEOJSON = FE_MAP_DATA / "taipei_green_shop.geojson"
NTPC_GEOJSON   = FE_MAP_DATA / "ntpc_green_stores.geojson"
METRO_GEOJSON  = FE_MAP_DATA / "metro_green_stores.geojson"

DB_HOST = os.getenv("DB_DASHBOARD_HOST", "postgres-data")
DB_PORT = os.getenv("DB_DASHBOARD_PORT", "5432")
DB_USER = os.getenv("DB_DASHBOARD_USER", "postgres")
DB_PASS = os.getenv("DB_DASHBOARD_PASSWORD", "12345678")
DB_NAME = os.getenv("DB_DASHBOARD_DBNAME", "dashboard")


# ─── DB helpers ────────────────────────────────────────────────────────────────

def build_engine():
    uri = f"postgresql+psycopg2://{DB_USER}:{DB_PASS}@{DB_HOST}:{DB_PORT}/{DB_NAME}"
    return create_engine(uri, future=True)


def ensure_table(engine, ddl: str):
    with engine.begin() as conn:
        conn.execute(text(ddl))


def replace_table(engine, table: str, df: pd.DataFrame, cols: list):
    rows = df[cols].replace({np.nan: None}).to_dict(orient="records")
    placeholders = ", ".join(f":{c}" for c in cols)
    col_str = ", ".join(cols)
    sql = text(f"INSERT INTO {table} ({col_str}) VALUES ({placeholders})")
    with engine.begin() as conn:
        conn.execute(text(f"TRUNCATE {table} RESTART IDENTITY"))
        conn.execute(sql, rows)


# ─── Taipei ─────────────────────────────────────────────────────────────────────

TAIPEI_TABLE = "taipei_green_shop"

TAIPEI_DDL = f"""
CREATE SEQUENCE IF NOT EXISTS {TAIPEI_TABLE}_ogc_fid_seq;

CREATE TABLE IF NOT EXISTS {TAIPEI_TABLE} (
    seqno   integer,
    name    text,
    address text,
    number  varchar(30),
    contact text,
    phone   varchar(60),
    ext     varchar(20),
    mobile  varchar(20),
    type    text,
    lng     double precision,
    lat     double precision,
    _ctime  timestamp DEFAULT CURRENT_TIMESTAMP,
    _mtime  timestamp,
    ogc_fid integer DEFAULT nextval('{TAIPEI_TABLE}_ogc_fid_seq') PRIMARY KEY
);

GRANT SELECT ON {TAIPEI_TABLE} TO PUBLIC;

CREATE OR REPLACE FUNCTION update_{TAIPEI_TABLE}_mtime()
RETURNS TRIGGER AS $$
BEGIN NEW._mtime = NOW(); RETURN NEW; END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_{TAIPEI_TABLE}_mtime ON {TAIPEI_TABLE};
CREATE TRIGGER trg_{TAIPEI_TABLE}_mtime
    BEFORE INSERT OR UPDATE ON {TAIPEI_TABLE}
    FOR EACH ROW EXECUTE FUNCTION update_{TAIPEI_TABLE}_mtime();
"""

TAIPEI_COLS = ["seqno", "name", "address", "number", "contact", "phone", "ext", "mobile", "type", "lng", "lat"]


def load_taipei() -> pd.DataFrame:
    df = pd.read_csv(TAIPEI_CSV, dtype=str)
    df.columns = ["seqno", "name", "address", "number", "contact", "phone", "ext", "mobile", "type", "lng", "lat"]
    df["seqno"] = pd.to_numeric(df["seqno"], errors="coerce")
    df["lng"]   = pd.to_numeric(df["lng"],   errors="coerce")
    df["lat"]   = pd.to_numeric(df["lat"],   errors="coerce")
    return df


# ─── New Taipei ─────────────────────────────────────────────────────────────────

NTPC_TABLE = "ntpc_green_stores"

NTPC_DDL = f"""
CREATE SEQUENCE IF NOT EXISTS {NTPC_TABLE}_ogc_fid_seq;

CREATE TABLE IF NOT EXISTS {NTPC_TABLE} (
    seqno            integer,
    type             text,
    city             varchar(20),
    countycode       varchar(10),
    name             text,
    address          text,
    number           varchar(30),
    localcallservice varchar(60),
    lng              double precision,
    lat              double precision,
    _ctime           timestamp DEFAULT CURRENT_TIMESTAMP,
    _mtime           timestamp,
    ogc_fid          integer DEFAULT nextval('{NTPC_TABLE}_ogc_fid_seq') PRIMARY KEY
);

GRANT SELECT ON {NTPC_TABLE} TO PUBLIC;

CREATE OR REPLACE FUNCTION update_{NTPC_TABLE}_mtime()
RETURNS TRIGGER AS $$
BEGIN NEW._mtime = NOW(); RETURN NEW; END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_{NTPC_TABLE}_mtime ON {NTPC_TABLE};
CREATE TRIGGER trg_{NTPC_TABLE}_mtime
    BEFORE INSERT OR UPDATE ON {NTPC_TABLE}
    FOR EACH ROW EXECUTE FUNCTION update_{NTPC_TABLE}_mtime();
"""

NTPC_COLS = ["seqno", "type", "city", "countycode", "name", "address", "number", "localcallservice", "lng", "lat"]


def load_ntpc() -> pd.DataFrame:
    df = pd.read_csv(NTPC_CSV, dtype=str)
    df = df.rename(columns={"經度": "lng", "緯度": "lat"})
    if "name" in df.columns:
        df["name"] = df["name"].str.strip()
    df["seqno"] = pd.to_numeric(df["seqno"], errors="coerce")
    df["lng"]   = pd.to_numeric(df["lng"],   errors="coerce")
    df["lat"]   = pd.to_numeric(df["lat"],   errors="coerce")
    return df


# ─── GeoJSON helpers ─────────────────────────────────────────────────────────────

def valid(val) -> bool:
    try:
        return not math.isnan(float(val))
    except (TypeError, ValueError):
        return False


def fmt_phone(phone, ext) -> str:
    p = str(phone) if (phone is not None and not (isinstance(phone, float) and math.isnan(phone))) else ""
    e = str(ext)   if (ext   is not None and not (isinstance(ext,   float) and math.isnan(ext)))   else ""
    # ext may be stored as "3961.0" — normalise to int string
    if e and e != "nan":
        try:
            e = str(int(float(e)))
        except ValueError:
            pass
        return f"{p}#{e}"
    return p


def taipei_features(df: pd.DataFrame) -> list:
    out = []
    for _, r in df.iterrows():
        if not (valid(r["lng"]) and valid(r["lat"])):
            continue
        out.append({
            "type": "Feature",
            "geometry": {"type": "Point", "coordinates": [float(r["lng"]), float(r["lat"])]},
            "properties": {
                "名稱":   r.get("name",    "") or "",
                "地址":   r.get("address", "") or "",
                "商店編號": r.get("number",  "") or "",
                "電話":   fmt_phone(r.get("phone"), r.get("ext")),
                "類型":   r.get("type",    "") or "",
            },
        })
    return out


def ntpc_features(df: pd.DataFrame) -> list:
    out = []
    for _, r in df.iterrows():
        if not (valid(r["lng"]) and valid(r["lat"])):
            continue
        out.append({
            "type": "Feature",
            "geometry": {"type": "Point", "coordinates": [float(r["lng"]), float(r["lat"])]},
            "properties": {
                "name":             (r.get("name",             "") or "").strip(),
                "type":             r.get("type",             "") or "",
                "address":          r.get("address",          "") or "",
                "number":           r.get("number",           "") or "",
                "localcallservice": r.get("localcallservice", "") or "",
            },
        })
    return out


def metro_features(tp_df: pd.DataFrame, ntpc_df: pd.DataFrame) -> list:
    """Unified schema with city field for the dual-metro combined layer."""
    out = []
    for _, r in tp_df.iterrows():
        if not (valid(r["lng"]) and valid(r["lat"])):
            continue
        out.append({
            "type": "Feature",
            "geometry": {"type": "Point", "coordinates": [float(r["lng"]), float(r["lat"])]},
            "properties": {
                "name":    r.get("name",    "") or "",
                "address": r.get("address", "") or "",
                "number":  r.get("number",  "") or "",
                "phone":   fmt_phone(r.get("phone"), r.get("ext")),
                "type":    r.get("type",    "") or "",
                "city":    "台北市",
            },
        })
    for _, r in ntpc_df.iterrows():
        if not (valid(r["lng"]) and valid(r["lat"])):
            continue
        out.append({
            "type": "Feature",
            "geometry": {"type": "Point", "coordinates": [float(r["lng"]), float(r["lat"])]},
            "properties": {
                "name":    (r.get("name",             "") or "").strip(),
                "address": r.get("address",           "") or "",
                "number":  r.get("number",            "") or "",
                "phone":   r.get("localcallservice",  "") or "",
                "type":    r.get("type",              "") or "",
                "city":    "新北市",
            },
        })
    return out


def write_geojson(path: Path, features: list):
    geojson = {
        "type": "FeatureCollection",
        "crs": {"type": "name", "properties": {"name": "urn:ogc:def:crs:OGC:1.3:CRS84"}},
        "features": features,
    }
    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, "w", encoding="utf-8") as f:
        json.dump(geojson, f, ensure_ascii=False, separators=(",", ":"))


# ─── Main ────────────────────────────────────────────────────────────────────────

def main():
    print("=== Green Shops Import ===\n")

    print("1) Reading CSVs...")
    tp_df   = load_taipei()
    ntpc_df = load_ntpc()
    print(f"   台北市: {len(tp_df)} rows | 新北市: {len(ntpc_df)} rows\n")

    # ── PostgreSQL ──
    print("2) Connecting to PostgreSQL...")
    db_ok = False
    if not _HAS_SQLALCHEMY:
        print("   sqlalchemy not installed — skipping DB insert.")
        print("   Install: pip install sqlalchemy psycopg2-binary\n")
    else:
        try:
            engine = build_engine()
            with engine.connect() as conn:
                conn.execute(text("SELECT 1"))
            print(f"   Connected → {DB_HOST}:{DB_PORT}/{DB_NAME}\n")
            db_ok = True
        except Exception as exc:
            print(f"   Connection failed: {exc}")
            print("   Skipping DB insert — GeoJSON will still be written.\n")

    if db_ok:
        print("3) Ensuring table schemas...")
        ensure_table(engine, TAIPEI_DDL)
        ensure_table(engine, NTPC_DDL)
        print("   OK\n")

        print("4) Truncating and inserting rows...")
        replace_table(engine, TAIPEI_TABLE, tp_df,   TAIPEI_COLS)
        print(f"   {len(tp_df)} rows → {TAIPEI_TABLE}")
        replace_table(engine, NTPC_TABLE,   ntpc_df, NTPC_COLS)
        print(f"   {len(ntpc_df)} rows → {NTPC_TABLE}\n")

    # ── GeoJSON ──
    step = 5 if db_ok else 3
    print(f"{step}) Generating GeoJSON files...")
    tp_feat   = taipei_features(tp_df)
    ntpc_feat = ntpc_features(ntpc_df)
    metro_feat = metro_features(tp_df, ntpc_df)

    write_geojson(TAIPEI_GEOJSON, tp_feat)
    print(f"   {len(tp_feat):>5} features → {TAIPEI_GEOJSON.name}")

    write_geojson(NTPC_GEOJSON, ntpc_feat)
    print(f"   {len(ntpc_feat):>5} features → {NTPC_GEOJSON.name}")

    write_geojson(METRO_GEOJSON, metro_feat)
    print(f"   {len(metro_feat):>5} features → {METRO_GEOJSON.name}")

    print("\nDone.")


if __name__ == "__main__":
    main()
