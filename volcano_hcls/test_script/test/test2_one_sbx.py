# main.py
from dotenv import load_dotenv
import os
load_dotenv()

os.environ['E2B_API_KEY'] = 'e2b_4291fc7c3dabdebbd3c0d12151d4e3762a55'
print(os.getenv("E2B_API_KEY"))
from e2b import Sandbox

#sbx1 = Sandbox.create("base",timeout=3600, allow_internet_access=True) # By default the sandbox is alive for 5 minutes
# sbx1 = Sandbox.create("9ym4hp71ewhx2c2l8srz",timeout=86400, allow_internet_access=True) # Bydefault the sandbox is alive for 5 minutes
sbx1 = Sandbox.create("9ym4hp71ewhx2c2l8srz",timeout=300, allow_internet_access=True) # By default the sandbox is alive for 5 minutes
print("creat success")
print(f"Sandbox ID: {sbx1.sandbox_id}")