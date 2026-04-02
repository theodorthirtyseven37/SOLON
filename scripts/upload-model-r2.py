#!/usr/bin/env python3
"""Download a GGUF model from HuggingFace and upload to Solon's R2 mirror."""

import os
import sys
import subprocess
import boto3

R2_ENDPOINT = "https://e8ac4eb5225e608e3a5a10015cce94fa.eu.r2.cloudflarestorage.com"
R2_BUCKET = "solon-models"

MODELS = {
    "llama3.2-3b-Q4_K_M.gguf": {
        "repo": "bartowski/Llama-3.2-3B-Instruct-GGUF",
        "pattern": "Q4_K_M",
        "hf_file": "Llama-3.2-3B-Instruct-Q4_K_M.gguf",
    },
}

def get_s3_client():
    key_id = os.environ.get("R2_ACCESS_KEY_ID")
    secret = os.environ.get("R2_SECRET_ACCESS_KEY")
    if not key_id or not secret:
        print("Error: Set R2_ACCESS_KEY_ID and R2_SECRET_ACCESS_KEY")
        sys.exit(1)
    return boto3.client("s3",
        endpoint_url=R2_ENDPOINT,
        aws_access_key_id=key_id,
        aws_secret_access_key=secret,
        region_name="auto",
    )

def upload_file(s3, local_path, r2_key):
    size = os.path.getsize(local_path)
    print(f"Uploading {r2_key} ({size / 1e9:.1f} GB)...")
    s3.upload_file(local_path, R2_BUCKET, r2_key,
        Callback=lambda bytes_transferred: None,
        Config=boto3.s3.transfer.TransferConfig(
            multipart_threshold=256 * 1024 * 1024,
            multipart_chunksize=256 * 1024 * 1024,
        ),
    )
    print(f"Done: https://pub-ceabcf6fa0bd445f944e5343aab8cd05.r2.dev/{r2_key}")

def download_from_hf(repo, filename, output_dir):
    """Download a specific file from HuggingFace using curl."""
    url = f"https://huggingface.co/{repo}/resolve/main/{filename}"
    output_path = os.path.join(output_dir, filename)
    if os.path.exists(output_path):
        print(f"Already downloaded: {output_path}")
        return output_path
    print(f"Downloading {url}...")
    subprocess.run(["curl", "-L", "-o", output_path, "--progress-bar", url], check=True)
    return output_path

if __name__ == "__main__":
    s3 = get_s3_client()
    tmp_dir = "/tmp/solon-models"
    os.makedirs(tmp_dir, exist_ok=True)

    # If specific model name given, only do that one
    target = sys.argv[1] if len(sys.argv) > 1 else None

    for r2_key, info in MODELS.items():
        if target and target != r2_key:
            continue
        local_path = download_from_hf(info["repo"], info["hf_file"], tmp_dir)
        upload_file(s3, local_path, r2_key)
