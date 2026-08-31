import asyncio
import sys
import os

from e2b import AsyncTemplate
from dotenv import load_dotenv
from e2b import Sandbox

load_dotenv()

os.environ['E2B_API_KEY'] = 'e2b_4291fc7c3dabdebbd3c0d12151d4e3762a55'
print(f"E2B_API_KEY from env: {os.getenv('E2B_API_KEY')}")

# Define the template, similar to `export const template = Template().fromBaseImage();`
# template = AsyncTemplate().from_base_image()
template = AsyncTemplate().from_image(
    image="mp-bp-cn-shanghai.cr.volces.com/e2b/ubuntu:22.04-s3",
    username="crrobot@infrawaves",
    password="Fikypjfqobu2"
)

# Build the template, similar to the main JS file
async def main():
    await AsyncTemplate.build(
        template,
        alias="ubuntu22_test_sll_2",
        cpu_count=1,
        memory_mb=1024,
        skip_cache=False,
        on_build_logs=lambda log: print(str(log)),
    )

if __name__ == "__main__":
    try:
        asyncio.run(main())
    except Exception as e:
        print(f"An error occurred: {e}", file=sys.stderr)