"""
YouTube API로 크리에이터 프로필 이미지 URL 업데이트
- profile_image가 NULL이거나 featuring.co URL인 YouTube 크리에이터 대상
- YouTube Data API v3 channel thumbnails 사용
- 배치 50개씩 처리 (API 할당량 절약)
"""

import psycopg2
import requests
import time
import os

# ========== 설정 (환경변수 우선) ==========
DB_CONFIG = {
    "host":     os.getenv("DB_HOST", "localhost"),
    "port":     int(os.getenv("DB_PORT", "5432")),
    "user":     os.getenv("DB_USER", "postgres"),
    "password": os.getenv("DB_PASSWORD", ""),
    "dbname":   os.getenv("DB_NAME", "postgres"),
}

YOUTUBE_API_KEY = os.getenv("YOUTUBE_API_KEY", "")
BATCH_SIZE = 50

# ==========================================

def get_channel_id_from_url(platform_url: str) -> str:
    """YouTube URL에서 channel_id 추출"""
    if not platform_url:
        return ""
    # https://www.youtube.com/channel/UCxxxxxx
    if "/channel/" in platform_url:
        return platform_url.split("/channel/")[-1].split("?")[0].strip()
    return ""

def fetch_thumbnails_batch(channel_ids: list) -> dict:
    """YouTube API로 채널 썸네일 URL 배치 조회"""
    if not channel_ids:
        return {}

    ids_param = ",".join(channel_ids)
    url = (
        "https://youtube.googleapis.com/youtube/v3/channels"
        f"?part=snippet&id={ids_param}&key={YOUTUBE_API_KEY}"
    )

    try:
        resp = requests.get(url, timeout=15)
        if resp.status_code != 200:
            print(f"  ⚠️ API 에러: {resp.status_code} {resp.text[:200]}")
            return {}

        data = resp.json()
        result = {}
        for item in data.get("items", []):
            cid = item["id"]
            thumbnails = item.get("snippet", {}).get("thumbnails", {})
            # medium > default 순으로 선택
            thumb = thumbnails.get("medium") or thumbnails.get("default")
            if thumb:
                result[cid] = thumb["url"]
        return result

    except Exception as e:
        print(f"  ❌ API 호출 실패: {e}")
        return {}

def main():
    print("🔗 DB 연결 중...")
    conn = psycopg2.connect(**DB_CONFIG)
    cur = conn.cursor()

    # 업데이트 대상 조회 (profile_image가 없거나 featuring.co URL인 YouTube)
    cur.execute("""
        SELECT id, platform_url, profile_image
        FROM creators
        WHERE platform = 'youtube'
          AND (
            profile_image IS NULL
            OR profile_image = ''
            OR profile_image LIKE '%featuring%'
            OR profile_image LIKE '%image.featuring%'
          )
        ORDER BY followers DESC
    """)

    rows = cur.fetchall()
    total = len(rows)
    print(f"📊 업데이트 대상: {total:,}건")

    if total == 0:
        print("✅ 업데이트할 항목 없음")
        return

    updated = 0
    skipped = 0

    # 배치로 처리
    for i in range(0, total, BATCH_SIZE):
        batch = rows[i:i + BATCH_SIZE]

        # channel_id 추출
        id_to_row = {}
        for row_id, platform_url, _ in batch:
            cid = get_channel_id_from_url(platform_url or "")
            if cid:
                id_to_row[cid] = row_id
            else:
                skipped += 1

        if not id_to_row:
            continue

        print(f"  [{i+1}~{min(i+BATCH_SIZE, total)}/{total}] API 호출 중...", end=" ", flush=True)

        thumbnails = fetch_thumbnails_batch(list(id_to_row.keys()))

        # DB 업데이트
        batch_updated = 0
        for cid, thumb_url in thumbnails.items():
            row_id = id_to_row.get(cid)
            if row_id and thumb_url:
                cur.execute(
                    "UPDATE creators SET profile_image = %s WHERE id = %s",
                    (thumb_url, row_id)
                )
                batch_updated += 1

        conn.commit()
        updated += batch_updated
        print(f"{batch_updated}건 업데이트")

        # API 할당량 보호 (초당 100 units, channels.list = 1 unit)
        time.sleep(0.2)

    cur.close()
    conn.close()

    print(f"\n✅ 완료: {updated:,}건 업데이트, {skipped:,}건 스킵 (channel_id 없음)")

if __name__ == "__main__":
    main()
