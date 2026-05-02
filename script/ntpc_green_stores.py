"""
Fetch all pages from NTPC green stores dataset, geocode each address,
and upsert the result into the dashboard postgres DB.

Usage (inside Docker network):
    docker run --rm --network br_dashboard \
        -v "$(pwd)/script:/script" \
        -e DB_DASHBOARD_HOST=postgres-data \
        python:3.11-slim bash -c \
        "pip install -q requests pandas sqlalchemy psycopg2-binary && python /script/ntpc_green_stores.py"

Usage (with host postgres port exposed, e.g. after temporarily adding 5433:5432 to postgres-data):
    DB_DASHBOARD_HOST=localhost DB_DASHBOARD_PORT=5433 python script/ntpc_green_stores.py
"""

import os
import re
import sys
import time

import pandas as pd
import requests
from io import StringIO
from sqlalchemy import create_engine, text

sys.path.insert(0, os.path.dirname(__file__))
from address_to_coordinate import get_coordinates

API_URL = "https://data.ntpc.gov.tw/api/datasets/6ccd0274-0c09-43b0-98fc-4d5222a71e8b/csv"
TABLE_NAME = "ntpc_green_stores"

DB_HOST = os.getenv("DB_DASHBOARD_HOST", "postgres-data")
DB_PORT = os.getenv("DB_DASHBOARD_PORT", "5432")
DB_USER = os.getenv("DB_DASHBOARD_USER", "postgres")
DB_PASS = os.getenv("DB_DASHBOARD_PASSWORD", "12345678")
DB_NAME = os.getenv("DB_DASHBOARD_DBNAME", "dashboard")

GEOCODE_DELAY = float(os.getenv("GEOCODE_DELAY", "0.2"))


def build_engine():
    uri = f"postgresql+psycopg2://{DB_USER}:{DB_PASS}@{DB_HOST}:{DB_PORT}/{DB_NAME}"
    return create_engine(uri, future=True)


def fetch_all_pages() -> pd.DataFrame:
    frames = []
    page = 0
    while True:
        resp = requests.get(
            API_URL,
            params={"page": page},
            headers={"accept": "text/csv;charset=UTF-8"},
            timeout=30,
        )
        resp.raise_for_status()
        text_body = resp.text.strip()
        if not text_body:
            break
        df = pd.read_csv(StringIO(text_body))
        if df.empty:
            break
        frames.append(df)
        print(f"  page {page}: {len(df)} rows")
        page += 1
        # API returns 30 rows per full page; fewer means this was the last page
        if len(df) < 30:
            break
    if not frames:
        raise RuntimeError("No data returned from API")
    return pd.concat(frames, ignore_index=True)


def clean_address(addr: str) -> str:
    # Strip leading 3-digit postal code (e.g. "220新北市…" → "新北市…")
    return re.sub(r"^\d{3}", "", str(addr)).strip()


def geocode_dataframe(df: pd.DataFrame) -> pd.DataFrame:
    lngs, lats = [], []
    total = len(df)
    for i, raw_addr in enumerate(df["address"], start=1):
        addr = clean_address(raw_addr)
        print(f"  [{i}/{total}] {addr}")
        try:
            loc = get_coordinates(addr)
            lngs.append(loc["x"] if loc else None)
            lats.append(loc["y"] if loc else None)
        except Exception as exc:
            print(f"    geocode error: {exc}")
            lngs.append(None)
            lats.append(None)
        time.sleep(GEOCODE_DELAY)
    df = df.copy()
    df["lng"] = lngs
    df["lat"] = lats
    return df


CREATE_TABLE_SQL = f"""
CREATE SEQUENCE IF NOT EXISTS {TABLE_NAME}_ogc_fid_seq;

CREATE TABLE IF NOT EXISTS {TABLE_NAME} (
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
    ogc_fid          integer DEFAULT nextval('{TABLE_NAME}_ogc_fid_seq') PRIMARY KEY
);

GRANT SELECT ON {TABLE_NAME} TO PUBLIC;

CREATE OR REPLACE FUNCTION update_{TABLE_NAME}_mtime()
RETURNS TRIGGER AS $$
BEGIN NEW._mtime = NOW(); RETURN NEW; END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_{TABLE_NAME}_mtime ON {TABLE_NAME};
CREATE TRIGGER trg_{TABLE_NAME}_mtime
    BEFORE INSERT OR UPDATE ON {TABLE_NAME}
    FOR EACH ROW EXECUTE FUNCTION update_{TABLE_NAME}_mtime();
"""

INSERT_COLS = [
    "seqno", "type", "city", "countycode",
    "name", "address", "number", "localcallservice",
    "lng", "lat",
]


def ensure_table(engine):
    with engine.begin() as conn:
        conn.execute(text(CREATE_TABLE_SQL))


def upsert_data(engine, df: pd.DataFrame):
    rows = df[INSERT_COLS].to_dict(orient="records")
    placeholders = ", ".join(f":{c}" for c in INSERT_COLS)
    cols = ", ".join(INSERT_COLS)
    insert_sql = text(
        f"INSERT INTO {TABLE_NAME} ({cols}) VALUES ({placeholders})"
    )
    with engine.begin() as conn:
        conn.execute(text(f"TRUNCATE {TABLE_NAME} RESTART IDENTITY"))
        conn.execute(insert_sql, rows)


def main():
    print("=== NTPC Green Stores Ingestion ===\n")

    print("1) Fetching data from NTPC API...")
    df = fetch_all_pages()
    print(f"   Total rows: {len(df)}\n")

    print("2) Geocoding addresses...")
    df = geocode_dataframe(df)
    geocoded = df["lng"].notna().sum()
    print(f"   Geocoded: {geocoded}/{len(df)}\n")

    print("3) Connecting to database...")
    engine = build_engine()
    # quick connectivity check
    with engine.connect() as conn:
        conn.execute(text("SELECT 1"))
    print(f"   Connected to {DB_HOST}:{DB_PORT}/{DB_NAME}\n")

    print("4) Ensuring table schema...")
    ensure_table(engine)

    print("5) Truncating and inserting rows...")
    upsert_data(engine, df)
    print(f"   Inserted {len(df)} rows into '{TABLE_NAME}'\n")

    print("Done.")


if __name__ == "__main__":
    main()
