import os
import requests
import pandas as pd

# ── 設定檔案路徑 ──────────────────────────────────────────
INPUT_PATH  = "C:\\Users\\qweas\\Desktop\\git-repos\\Taipei-City-Dashboard\\taipei_green_shop.csv"   # 貼上 CSV 檔案路徑，例如 r"C:\\data\\名單.csv"
OUTPUT_PATH = ""   # 留空則自動產生（與輸入檔案同目錄，檔名加 _coordinates）
# ─────────────────────────────────────────────────────────

BASE_URL = "https://geocode.arcgis.com/arcgis/rest/services/World/GeocodeServer/findAddressCandidates"

def get_coordinates(address):
    params = {
        'SingleLine': address,
        'f': 'json',
        'outSR': '{"wkid":4326}',
        'outFields': 'Addr_type,Match_addr,StAddr,City',
        'maxLocations': 6
    }

    response = requests.get(BASE_URL, params=params)

    if response.status_code == 200:
        data = response.json()
        if data['candidates']:
            return data['candidates'][0]['location']
        else:
            return None
    else:
        response.raise_for_status()


def process_csv(input_path, output_path):
    print(f"讀取檔案：{input_path}")
    df = pd.read_csv(input_path, encoding='utf-8-sig')

    if '聯絡地址' not in df.columns:
        raise ValueError("找不到「聯絡地址」欄位，請確認欄位名稱是否正確。")

    lngs, lats = [], []
    total = len(df)

    for i, address in enumerate(df['聯絡地址'], start=1):
        print(f"[{i}/{total}] 轉換中：{address}")
        if pd.isna(address) or str(address).strip() == '':
            lngs.append(None)
            lats.append(None)
            continue
        try:
            location = get_coordinates(str(address).strip())
            if location:
                lngs.append(location['x'])
                lats.append(location['y'])
            else:
                lngs.append(None)
                lats.append(None)
        except Exception as e:
            print(f"  ⚠ 轉換失敗：{e}")
            lngs.append(None)
            lats.append(None)

    df['經度'] = lngs
    df['緯度'] = lats
    df.to_csv(output_path, index=False, encoding='utf-8-sig')
    print(f"\n完成！結果已儲存至：{output_path}")


if __name__ == "__main__":
    if not INPUT_PATH:
        print("錯誤：請在腳本頂端的 INPUT_PATH 填入 CSV 檔案路徑。")
        exit(1)

    if not os.path.exists(INPUT_PATH):
        print(f"錯誤：找不到檔案 {INPUT_PATH}")
        exit(1)

    output_path = OUTPUT_PATH if OUTPUT_PATH else os.path.splitext(INPUT_PATH)[0] + '_coordinates.csv'

    process_csv(INPUT_PATH, output_path)
