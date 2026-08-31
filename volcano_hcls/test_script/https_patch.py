"""HTTPS 补丁：让 e2b SDK 在私有化部署下始终使用 HTTPS"""
from e2b.connection_config import ConnectionConfig


def patch_https():
    def _get_sandbox_url(self, sandbox_id, sandbox_domain):
        url = f"https://{self.get_host(sandbox_id, sandbox_domain, self.envd_port)}"
        return url

    ConnectionConfig.get_sandbox_url = _get_sandbox_url
