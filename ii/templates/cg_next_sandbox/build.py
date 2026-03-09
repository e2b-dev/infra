from dotenv import load_dotenv
from e2b import Template, default_build_logger, wait_for_port

load_dotenv()

template = (
    Template()
    .from_gcp_registry(
        image="us-central1-docker.pkg.dev/backend-alpha-97077/iirepo/cg-next-sandbox:latest",
        service_account_json="../../secrets/backend-alpha-97077-661f5c593bf3.json",
    )
    .set_user("root")
    .set_workdir("/root")
    .set_start_cmd("/root/.jupyter/start-up.sh", wait_for_port(49999))
)

if __name__ == "__main__":
    Template.build(
        template,
        alias="cg_next_sandbox",
        on_build_logs=default_build_logger(),
        cpu_count=1,
        memory_mb=1024,
    )