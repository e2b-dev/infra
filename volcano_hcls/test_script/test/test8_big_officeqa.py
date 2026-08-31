# 大镜像构建模版

import asyncio
import sys
import os

from e2b import AsyncTemplate
from dotenv import load_dotenv
from e2b import Sandbox

load_dotenv()
print(f"E2B_API_KEY from env: {os.getenv('E2B_API_KEY')}")

template = AsyncTemplate().from_image(
    image="mp-bp-cn-shanghai.cr.volces.com/north-prod-images/officeqa-v3:latest",
    username="crrobot@infrawaves",
    password="Fikypjfqobu2"
)

# Build the template, similar to the main JS file
async def main():
    await AsyncTemplate.build(
        template,
        alias="officeqa-v3-test_202629",
        cpu_count=1,
        memory_mb=1024,
        skip_cache=True,
        on_build_logs=lambda log: print(str(log)),
    )

if __name__ == "__main__":
    try:
        asyncio.run(main())
    except Exception as e:
        print(f"An error occurred: {e}", file=sys.stderr)