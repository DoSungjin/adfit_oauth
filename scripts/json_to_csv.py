"""
JSON 크리에이터 데이터 -> CSV 변환 스크립트 (스트리밍 방식)
출력: creators_youtube.csv, creators_instagram.csv, creators_tiktok.csv
"""

import ijson
import csv
import os

BASE_DIR = r"D:\Budit\01_services\adtown\backend\db\Creator_Data"
OUT_DIR  = r"D:\Budit\01_services\adtown\backend\scripts\output"
os.makedirs(OUT_DIR, exist_ok=True)

FIELDS = [
    "platform", "handle", "name", "profile_image", "category",
    "followers", "engagement_rate", "language",
    "avg_views", "avg_likes", "avg_comments",
    "audience_gender", "audience_age",
    "estimated_ad_cost", "last_upload",
    "platform_url", "featuring_url", "email", "crawled_at",
]

def safe_int(v):
    try:
        return int(v) if v not in (None, "", "-") else 0
    except:
        return 0

def safe_str(v):
    if v is None:
        return ""
    return str(v).replace("\x00", "")

def to_row_youtube(d):
    return {
        "platform":        "youtube",
        "handle":          safe_str(d.get("handle")),
        "name":            safe_str(d.get("name")),
        "profile_image":   safe_str(d.get("profile_image")),
        "category":        safe_str(d.get("category")),
        "followers":       safe_int(d.get("subscribers")),
        "engagement_rate": safe_str(d.get("engagement_rate")),
        "language":        safe_str(d.get("language")),
        "avg_views":       safe_int(d.get("avg_video_views")),
        "avg_likes":       safe_int(d.get("avg_likes")),
        "avg_comments":    safe_int(d.get("avg_comments")),
        "audience_gender": safe_str(d.get("audience_gender")),
        "audience_age":    safe_str(d.get("audience_age")),
        "estimated_ad_cost": safe_str(d.get("estimated_ad_cost")),
        "last_upload":     safe_str(d.get("last_upload")),
        "platform_url":    safe_str(d.get("youtube_url")),
        "featuring_url":   safe_str(d.get("featuring_url")),
        "email":           safe_str(d.get("email")),
        "crawled_at":      safe_str(d.get("crawled_at")),
    }

def to_row_instagram(d):
    return {
        "platform":        "instagram",
        "handle":          safe_str(d.get("handle")),
        "name":            safe_str(d.get("name")),
        "profile_image":   safe_str(d.get("profile_image")),
        "category":        safe_str(d.get("category")),
        "followers":       safe_int(d.get("followers")),
        "engagement_rate": safe_str(d.get("engagement_rate")),
        "language":        safe_str(d.get("language")),
        "avg_views":       safe_int(d.get("avg_video_views")),
        "avg_likes":       safe_int(d.get("avg_likes")),
        "avg_comments":    safe_int(d.get("avg_comments")),
        "audience_gender": safe_str(d.get("audience_gender")),
        "audience_age":    safe_str(d.get("audience_age")),
        "estimated_ad_cost": safe_str(d.get("estimated_ad_cost")),
        "last_upload":     safe_str(d.get("last_upload")),
        "platform_url":    safe_str(d.get("instagram_url")),
        "featuring_url":   safe_str(d.get("featuring_url")),
        "email":           "",
        "crawled_at":      safe_str(d.get("crawled_at")),
    }

def to_row_tiktok(d):
    return {
        "platform":        "tiktok",
        "handle":          safe_str(d.get("handle")),
        "name":            safe_str(d.get("name")),
        "profile_image":   safe_str(d.get("profile_image")),
        "category":        safe_str(d.get("category")),
        "followers":       safe_int(d.get("followers")),
        "engagement_rate": safe_str(d.get("engagement_rate")),
        "language":        safe_str(d.get("language")),
        "avg_views":       safe_int(d.get("avg_views")),
        "avg_likes":       safe_int(d.get("avg_likes")),
        "avg_comments":    safe_int(d.get("avg_comments")),
        "audience_gender": safe_str(d.get("audience_gender")),
        "audience_age":    safe_str(d.get("audience_age")),
        "estimated_ad_cost": safe_str(d.get("estimated_ad_cost")),
        "last_upload":     safe_str(d.get("last_upload")),
        "platform_url":    safe_str(d.get("tiktok_url")),
        "featuring_url":   safe_str(d.get("featuring_url")),
        "email":           "",
        "crawled_at":      safe_str(d.get("crawled_at")),
    }

FILES = [
    ("youtube_master.json",   to_row_youtube,   "creators_youtube.csv"),
    ("instagram_master.json", to_row_instagram, "creators_instagram.csv"),
    ("tiktok_master.json",    to_row_tiktok,    "creators_tiktok.csv"),
]

for filename, converter, outname in FILES:
    filepath = os.path.join(BASE_DIR, filename)
    outpath  = os.path.join(OUT_DIR, outname)

    print(f"streaming: {filename} ...", flush=True)

    count = 0
    with open(filepath, "rb") as f_in, \
         open(outpath, "w", newline="", encoding="utf-8") as f_out:

        writer = csv.DictWriter(f_out, fieldnames=FIELDS, extrasaction='ignore')
        # 헤더 없이 저장 (Cloud SQL import용)

        for item in ijson.items(f_in, "item"):
            row = converter(item)
            writer.writerow(row)
            count += 1
            if count % 10000 == 0:
                print(f"  {count:,}...", flush=True)

    size_mb = os.path.getsize(outpath) / 1024 / 1024
    print(f"done: {outname} ({count:,}rows, {size_mb:.1f}MB)")

print("all done!")
