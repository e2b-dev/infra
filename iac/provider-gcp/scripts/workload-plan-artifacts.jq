def planned_resources:
  [
    (
      .planned_values.root_module
      | recurse(.child_modules[]?)
      | .resources[]?
    )
  ];

def managed_changes:
  [
    .resource_changes[]?
    | select(.mode == "managed")
  ];

def normalize_gce_image:
  if type == "string" then
    sub("^https://www.googleapis.com/compute/v1/"; "")
  else
    null
  end;

def orchestrator_data_addresses:
  [
    "module.cluster.data.google_compute_image.server_source_image",
    "module.cluster.data.google_compute_image.api_source_image",
    "module.cluster.data.google_compute_image.clickhouse_source_image",
    "module.cluster.data.google_compute_image.loki_source_image",
    "module.cluster.module.build_cluster[\"default\"].data.google_compute_image.source_image",
    "module.cluster.module.client_cluster[\"default\"].data.google_compute_image.source_image"
  ];

def instance_template_addresses:
  [
    "module.cluster.google_compute_instance_template.server",
    "module.cluster.google_compute_instance_template.api",
    "module.cluster.google_compute_instance_template.clickhouse",
    "module.cluster.google_compute_instance_template.loki",
    "module.cluster.module.build_cluster[\"default\"].google_compute_instance_template.template",
    "module.cluster.module.client_cluster[\"default\"].google_compute_instance_template.template"
  ];

def core_specs:
  [
    {
      image: "api",
      address: "module.nomad.data.google_artifact_registry_docker_image.api_image"
    },
    {
      image: "db-migrator",
      address: "module.nomad.data.google_artifact_registry_docker_image.db_migrator_image"
    },
    {
      image: "docker-reverse-proxy",
      address: "module.nomad.data.google_artifact_registry_docker_image.docker_reverse_proxy_image"
    },
    {
      image: "client-proxy",
      address: "module.nomad.data.google_artifact_registry_docker_image.client_proxy_image"
    },
    {
      image: "clickhouse-migrator",
      address: "module.nomad.data.google_artifact_registry_docker_image.clickhouse_migrator_image"
    }
  ];

def job_specs:
  [
    {
      address: "module.nomad.module.api.nomad_job.api",
      images: ["api", "db-migrator"]
    },
    {
      address: "module.nomad.nomad_job.docker_reverse_proxy",
      images: ["docker-reverse-proxy"]
    },
    {
      address: "module.nomad.module.client_proxy.nomad_job.client_proxy",
      images: ["client-proxy"]
    }
  ];

planned_resources as $planned
| managed_changes as $changes
| orchestrator_data_addresses as $orchestrator_addresses
| instance_template_addresses as $template_addresses
| core_specs as $core_specs
| job_specs as $job_specs
| (
    [
      $planned[]
      | . as $resource
      | select(
          .mode == "data"
          and .type == "google_compute_image"
          and ($orchestrator_addresses | index($resource.address)) != null
        )
    ]
  ) as $orchestrator_rows
| (
    [
      $planned[]
      | . as $resource
      | select(
          .mode == "data"
          and .type == "google_artifact_registry_docker_image"
          and (
            [
              $core_specs[].address
            ]
            | index($resource.address)
          ) != null
        )
    ]
  ) as $core_rows
| {
    missing_or_duplicate_orchestrator_images: [
      $orchestrator_addresses[] as $address
      | (
          [
            $orchestrator_rows[]
            | select(.address == $address)
          ]
          | length
        ) as $count
      | select($count != 1)
      | {
          address: $address,
          count: $count
        }
    ],
    invalid_orchestrator_images: [
      $orchestrator_rows[]
      | select(
          .values.family != $artifacts.orchestrator_image.family
          or .values.name != $artifacts.orchestrator_image.name
          or .values.project != $artifacts.orchestrator_image.project
          or .values.status != $artifacts.orchestrator_image.status
          or (
            .values.self_link
            | normalize_gce_image
          ) != (
            $artifacts.orchestrator_image.self_link
            | normalize_gce_image
          )
          or (
            .values.id
            | normalize_gce_image
          ) != (
            $artifacts.orchestrator_image.self_link
            | normalize_gce_image
          )
        )
      | {
          address,
          values
        }
    ],
    invalid_template_source_images: [
      $changes[]
      | . as $resource
      | select(
          .type == "google_compute_instance_template"
          and ($template_addresses | index($resource.address)) != null
        )
      | (
          [
            .change.after.disk[]?
            | select(.boot == true)
            | .source_image
          ]
        ) as $source_images
      | select(
          ($source_images | length) != 1
          or (
            $source_images[0]
            | normalize_gce_image
          ) != (
            $artifacts.orchestrator_image.self_link
            | normalize_gce_image
          )
        )
      | {
          address,
          source_images: $source_images
        }
    ],
    missing_or_duplicate_core_images: [
      $core_specs[] as $spec
      | (
          [
            $core_rows[]
            | select(.address == $spec.address)
          ]
          | length
        ) as $count
      | select($count != 1)
      | {
          address: $spec.address,
          image: $spec.image,
          count: $count
        }
    ],
    invalid_core_images: [
      $core_specs[] as $spec
      | $core_rows[]
      | select(.address == $spec.address)
      | (
          "projects/"
          + $artifacts.gcp_project_id
          + "/locations/"
          + $artifacts.gcp_region
          + "/repositories/"
          + $artifacts.core_repository
          + "/dockerImages/"
          + $spec.image
          + "@"
          + $artifacts.core_images[$spec.image].latest.digest
        ) as $expected_name
      | select(
          .values.image_name != ($spec.image + ":latest")
          or .values.location != $artifacts.gcp_region
          or .values.repository_id != $artifacts.core_repository
          or .values.self_link
            != $artifacts.core_images[$spec.image].latest.resolved_reference
          or .values.name != $expected_name
        )
      | {
          address,
          image: $spec.image,
          values
        }
    ],
    missing_or_duplicate_core_jobs: [
      $job_specs[] as $spec
      | (
          [
            $changes[]
            | select(
                .type == "nomad_job"
                and .address == $spec.address
              )
          ]
          | length
        ) as $count
      | select($count != 1)
      | {
          address: $spec.address,
          count: $count
        }
    ],
    invalid_core_jobs: [
      $job_specs[] as $spec
      | $changes[]
      | select(
          .type == "nomad_job"
          and .address == $spec.address
        )
      | . as $job
      | (
          [
            $spec.images[]
            | $artifacts.core_images[.].latest.resolved_reference
          ]
          | sort
        ) as $expected_images
      | (
          if ($job.change.after.jobspec | type) == "string" then
            [
              $job.change.after.jobspec
              | scan(
                  "(?m)^[[:space:]]*image[[:space:]]*=[[:space:]]*\"([^\"]+)\""
                )
              | .[0]
            ]
            | sort
          else
            []
          end
        ) as $actual_images
      | select($actual_images != $expected_images)
      | {
          address: $spec.address,
          expected_images: $expected_images,
          actual_images: $actual_images
        }
    ]
  }
