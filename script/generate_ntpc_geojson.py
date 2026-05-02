"""
Generate ntpc_green_stores.geojson from the dashboard postgres DB
and write it to FE/public/mapData/.

Run inside Docker network (postgres-data is not host-exposed):
    docker run --rm --network br_dashboard \
        -v "$(pwd):/project" \
        python:3.11-slim \
        bash -c "pip install -q psycopg2-binary && python /project/script/generate_ntpc_geojson.py"
"""

import json
import os
import sys

import psycopg2

DB_HOST = os.getenv("DB_DASHBOARD_HOST", "postgres-data")
DB_PORT = os.getenv("DB_DASHBOARD_PORT", "5432")
DB_USER = os.getenv("DB_DASHBOARD_USER", "postgres")
DB_PASS = os.getenv("DB_DASHBOARD_PASSWORD", "12345678")
DB_NAME = os.getenv("DB_DASHBOARD_DBNAME", "dashboard")

OUTPUT_PATH = os.getenv(
    "GEOJSON_OUTPUT",
    "/project/Taipei-City-Dashboard-FE/public/mapData/ntpc_green_stores.geojson",
)


def main():
    print(f"Connecting to {DB_HOST}:{DB_PORT}/{DB_NAME}...")
    conn = psycopg2.connect(
        host=DB_HOST, port=DB_PORT, user=DB_USER, password=DB_PASS, dbname=DB_NAME
    )
    cur = conn.cursor()

    cur.execute("SELECT COUNT(*) FROM ntpc_green_stores WHERE lng IS NOT NULL")
    total = cur.fetchone()[0]
    print(f"Rows with coordinates: {total}")

    cur.execute("""
        SELECT
            name, type, address, number, localcallservice,
            lng, lat
        FROM ntpc_green_stores
        WHERE lng IS NOT NULL AND lat IS NOT NULL
        ORDER BY seqno
    """)
    rows = cur.fetchall()

    features = []
    for name, store_type, address, number, localcallservice, lng, lat in rows:
        features.append({
            "type": "Feature",
            "properties": {
                "name":             name,
                "type":             store_type,
                "address":          address,
                "number":           number,
                "localcallservice": localcallservice,
            },
            "geometry": {
                "type": "Point",
                "coordinates": [lng, lat],
            },
        })

    geojson = {
        "type": "FeatureCollection",
        "crs": {
            "type": "name",
            "properties": {"name": "urn:ogc:def:crs:OGC:1.3:CRS84"},
        },
        "features": features,
    }

    cur.close()
    conn.close()

    os.makedirs(os.path.dirname(OUTPUT_PATH), exist_ok=True)
    with open(OUTPUT_PATH, "w", encoding="utf-8") as f:
        json.dump(geojson, f, ensure_ascii=False, separators=(",", ":"))

    print(f"Written {len(features)} features → {OUTPUT_PATH}")


if __name__ == "__main__":
    main()
