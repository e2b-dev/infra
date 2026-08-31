# 并发构建模版
import asyncio
import sys
import os

from e2b import AsyncTemplate
from dotenv import load_dotenv

load_dotenv()
print(f"E2B_API_KEY from env: {os.getenv('E2B_API_KEY')}")

IMAGE = "mp-bp-cn-shanghai.cr.volces.com/e2b/ubuntu:22.04-s3"
USERNAME = "crrobot@infrawaves"
PASSWORD = "Fikypjfqobu2"
CONCURRENCY = 10


async def build_one(alias: str) -> str:
    template = AsyncTemplate().from_image(
        image=IMAGE,
        username=USERNAME,
        password=PASSWORD,
    )
    await AsyncTemplate.build(
        template,
        alias=alias,
        cpu_count=1,
        memory_mb=1024,
        skip_cache=False,
        on_build_logs=lambda log, a=alias: print(f"[{a}] {log}"),
    )
    return alias


async def main():
    aliases = [f"test-202629-{i}" for i in range(1, CONCURRENCY + 1)]
    print(f"Building {CONCURRENCY} templates: {aliases}")
    tasks = [build_one(a) for a in aliases]
    results = await asyncio.gather(*tasks, return_exceptions=True)
    for alias, r in zip(aliases, results):
        if isinstance(r, Exception):
            print(f"[{alias}] failed: {r}", file=sys.stderr)
        else:
            print(f"[{alias}] done")


if __name__ == "__main__":
    try:
        asyncio.run(main())
    except Exception as e:
        print(f"An error occurred: {e}", file=sys.stderr)
        sys.exit(1)
