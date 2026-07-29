def planned_resources:
  [
    (
      .planned_values.root_module
      | recurse(.child_modules[]?)
      | .resources[]?
    )
  ];

def prior_resources:
  [
    (
      .prior_state.values.root_module?
      | select(. != null)
      | recurse(.child_modules[]?)
      | .resources[]?
    )
  ];

def all_changes:
  [
    .resource_changes[]?
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
      address: "module.nomad.data.google_artifact_registry_docker_image.api_image",
      name: "api_image"
    },
    {
      image: "db-migrator",
      address: "module.nomad.data.google_artifact_registry_docker_image.db_migrator_image",
      name: "db_migrator_image"
    },
    {
      image: "docker-reverse-proxy",
      address: "module.nomad.data.google_artifact_registry_docker_image.docker_reverse_proxy_image",
      name: "docker_reverse_proxy_image"
    },
    {
      image: "client-proxy",
      address: "module.nomad.data.google_artifact_registry_docker_image.client_proxy_image",
      name: "client_proxy_image"
    },
    {
      image: "clickhouse-migrator",
      address: "module.nomad.data.google_artifact_registry_docker_image.clickhouse_migrator_image",
      name: "clickhouse_migrator_image"
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

def job_binary_specs:
  [
    {
      binary: "orchestrator",
      data_name: "orchestrator",
      data_address: "module.nomad.data.google_storage_bucket_object.orchestrator[0]",
      job_address: "module.nomad.module.orchestrator[0].nomad_job.orchestrator",
      required_job: true
    },
    {
      binary: "template-manager",
      data_name: "template_manager",
      data_address: "module.nomad.data.google_storage_bucket_object.template_manager",
      job_address: "module.nomad.module.template_manager.nomad_job.template_manager",
      required_job: true,
      allow_unknown_jobspec: true,
      module_name: "template_manager",
      module_source: "../../modules/job-template-manager",
      artifact_source_reference: "local.template_manager_artifact_source"
    },
    {
      binary: "clean-nfs-cache",
      data_name: "filestore_cleanup",
      data_address: "module.nomad.data.google_storage_bucket_object.filestore_cleanup",
      job_address: "module.nomad.nomad_job.clean_nfs_cache[0]",
      required_job: false
    }
  ];

planned_resources as $planned
| prior_resources as $prior
| all_changes as $all_changes
| managed_changes as $changes
| orchestrator_data_addresses as $orchestrator_addresses
| instance_template_addresses as $template_addresses
| core_specs as $core_specs
| job_specs as $job_specs
| job_binary_specs as $job_binary_specs
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
    .configuration.root_module.module_calls.nomad? // {}
  ) as $nomad_config
| (
    .configuration.provider_config? // {}
  ) as $provider_configs
| (
    [
      $core_specs[] as $spec
      | (
          [
            $planned[]
            | select(.address == $spec.address)
          ]
        ) as $planned_rows
      | (
          [
            $prior[]
            | select(.address == $spec.address)
          ]
        ) as $prior_rows
      | (
          [
            $all_changes[]
            | select(.address == $spec.address)
          ]
        ) as $change_rows
      | (
          [
            $nomad_config.module.resources[]?
            | select(
                .address
                == (
                  "data.google_artifact_registry_docker_image."
                  + $spec.name
                )
              )
          ]
        ) as $config_rows
      | {
          spec: $spec,
          planned_count: ($planned_rows | length),
          prior_count: ($prior_rows | length),
          change_count: ($change_rows | length),
          config_count: ($config_rows | length),
          config: $config_rows[0],
          row: (
            if ($planned_rows | length) == 1 then
              $planned_rows[0]
            elif (
              ($planned_rows | length) == 0
              and ($prior_rows | length) == 1
              and ($change_rows | length) == 0
            ) then
              $prior_rows[0]
            else
              null
            end
          )
        }
    ]
  ) as $core_selections
| (
    [
      $job_binary_specs[] as $spec
      | (
          [
            $planned[]
            | select(.address == $spec.data_address)
          ]
        ) as $planned_rows
      | (
          [
            $prior[]
            | select(.address == $spec.data_address)
          ]
        ) as $prior_rows
      | (
          [
            $all_changes[]
            | select(.address == $spec.data_address)
          ]
        ) as $change_rows
      | (
          [
            $nomad_config.module.resources[]?
            | select(
                .mode == "data"
                and .type == "google_storage_bucket_object"
                and .name == $spec.data_name
              )
          ]
        ) as $config_rows
      | {
          spec: $spec,
          planned_count: ($planned_rows | length),
          prior_count: ($prior_rows | length),
          change_count: ($change_rows | length),
          config_count: ($config_rows | length),
          config: $config_rows[0],
          row: (
            if ($planned_rows | length) == 1 then
              $planned_rows[0]
            elif (
              ($planned_rows | length) == 0
              and ($prior_rows | length) == 1
              and ($change_rows | length) == 0
            ) then
              $prior_rows[0]
            else
              null
            end
          )
        }
    ]
  ) as $job_binary_selections
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
      # Terraform omits data sources that it fully resolves during planning
      # from planned_values. The instance-template source-image checks below
      # remain the canonical binding; reject only ambiguous duplicate rows here.
      | select($count > 1)
      | {
          address: $address,
          count: $count
        }
    ],
    invalid_orchestrator_images: [
      $orchestrator_rows[]
      | select(
        .values.family != $artifacts.orchestrator_image.family
          or .values.project != $artifacts.orchestrator_image.project
          or (
            # planned_values retains only configuration fields for data
            # sources that Terraform defers. Validate computed identity when
            # present; template source_image remains mandatory below.
            (.values | has("name"))
            and (
              .values.name != $artifacts.orchestrator_image.name
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
      | (
          if (
            $resource.address
            == "module.cluster.module.build_cluster[\"default\"].google_compute_instance_template.template"
          ) then
            "module.cluster.module.build_cluster[\"default\"].data.google_compute_image.source_image"
          elif (
            $resource.address
            == "module.cluster.module.client_cluster[\"default\"].google_compute_instance_template.template"
          ) then
            "module.cluster.module.client_cluster[\"default\"].data.google_compute_image.source_image"
          else
            null
          end
        ) as $deferred_data_address
      | select(
        ($source_images | length) != 1
          or (
            $source_images[0]
            | normalize_gce_image
          ) != (
            $artifacts.orchestrator_image.self_link
            | normalize_gce_image
          )
          and (
            $source_images[0] != null
            or $deferred_data_address == null
            or (
              [
                $orchestrator_rows[]
                | select(.address == $deferred_data_address)
                | select(
                    .values.family
                    == $artifacts.orchestrator_image.family
                    and .values.project
                    == $artifacts.orchestrator_image.project
                  )
              ]
              | length
            ) != 1
            or (
              [
                range(
                  0;
                  (($resource.change.after.disk // []) | length)
                ) as $disk_index
                | select(
                    $resource.change.after.disk[$disk_index].boot == true
                  )
                | select(
                    (
                      $resource.change.after_unknown.disk[$disk_index].source_image
                      // false
                    ) == true
                  )
              ]
              | length
            ) != 1
          )
        )
      | {
          address,
          source_images: $source_images
        }
    ],
    missing_or_duplicate_core_images: [
      $core_selections[]
      | select(
          .planned_count > 1
          or .prior_count > 1
          or .change_count > 0
          or (
            .planned_count == 0
            and .prior_count != 1
          )
        )
      | {
          address: .spec.address,
          image: .spec.image,
          planned_count,
          prior_count,
          change_count
        }
    ],
    invalid_core_images: [
      $core_selections[]
      | select(.row != null)
      | . as $selection
      | (
          "projects/"
          + $artifacts.gcp_project_id
          + "/locations/"
          + $artifacts.gcp_region
          + "/repositories/"
          + $artifacts.core_repository
          + "/dockerImages/"
          + $selection.spec.image
          + "@"
          + $artifacts.core_images[$selection.spec.image].latest.digest
        ) as $expected_name
      | (
          $provider_configs[
            ($selection.config.provider_config_key // "")
          ] // {}
        ) as $provider_config
      | select(
          .config_count != 1
          or .row.address != .spec.address
          or .row.mode != "data"
          or .row.type != "google_artifact_registry_docker_image"
          or .row.name != .spec.name
          or .row.values.image_name != (.spec.image + ":latest")
          or .row.values.location != $artifacts.gcp_region
          or .row.values.repository_id != $artifacts.core_repository
          or .row.values.self_link
            != $artifacts.core_images[.spec.image].latest.resolved_reference
          or .row.values.name != $expected_name
          or .config.mode != "data"
          or .config.type != "google_artifact_registry_docker_image"
          or .config.name != .spec.name
          or .config.expressions.image_name.constant_value
            != (.spec.image + ":latest")
          or .config.expressions.location.references != ["var.gcp_region"]
          or .config.expressions.repository_id.references
            != ["var.core_repository_name"]
          or $nomad_config.expressions.gcp_project_id.references
            != ["var.gcp_project_id"]
          or $nomad_config.expressions.gcp_region.references
            != ["var.gcp_region"]
          or (
            $nomad_config.expressions.core_repository_name.references
              != ["module.init.core_repository_name"]
            and (
              (
                $nomad_config.expressions.core_repository_name.references
                // []
              )
              | sort
            ) != (
              ["module.init", "module.init.core_repository_name"]
              | sort
            )
          )
          or $provider_config.full_name
            != "registry.terraform.io/hashicorp/google"
          or ($provider_config.alias // null) != null
          or $provider_config.expressions.project.references
            != ["var.gcp_project_id"]
          or $provider_config.expressions.region.references
            != ["var.gcp_region"]
        )
      | {
          address: .spec.address,
          image: .spec.image,
          reason: "effective core-image identity or Terraform wiring mismatch"
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
          $core_job_images[$spec.address] // []
          | sort
        ) as $actual_images
      | select(
          (
            $job.change.actions != ["create"]
            and $job.change.actions != ["update"]
            and $job.change.actions != ["no-op"]
          )
          or ($job.change.after.jobspec | type) != "string"
          or ($job.change.after_unknown.jobspec // false) == true
          or ($actual_images | length) != ($expected_images | length)
          or $actual_images != $expected_images
        )
      | {
          address: $spec.address,
          expected_images: $expected_images,
          actual_images: $actual_images
        }
    ],
    missing_or_duplicate_job_binary_objects: [
      $job_binary_selections[]
      | select(
          .planned_count > 1
          or .prior_count > 1
          or .change_count > 0
          or (
            .planned_count == 0
            and .prior_count != 1
          )
        )
      | {
          address: .spec.data_address,
          binary: .spec.binary,
          planned_count,
          prior_count,
          change_count
        }
    ],
    invalid_job_binary_objects: [
      $job_binary_selections[]
      | select(.row != null)
      | . as $selection
      | (
          $provider_configs[
            ($selection.config.provider_config_key // "")
          ] // {}
        ) as $provider_config
      | select(
          .config_count != 1
          or .row.address != .spec.data_address
          or .row.mode != "data"
          or .row.type != "google_storage_bucket_object"
          or .row.name != .spec.data_name
          or .row.values.bucket
            != $artifacts.job_binaries[.spec.binary].revision.bucket
          or .row.values.name
            != $artifacts.job_binaries[.spec.binary].revision.name
          or (.row.values.generation | tostring)
            != $artifacts.job_binaries[.spec.binary].revision.generation
          or .row.values.md5hash
            != $artifacts.job_binaries[.spec.binary].revision.md5
          or .row.values.crc32c
            != $artifacts.job_binaries[.spec.binary].revision.crc32c
          or .config.expressions.bucket.references
            != ["var.fc_env_pipeline_bucket_name"]
          or .config.expressions.name.references
            != ["local.job_binary_suffix"]
          or $provider_config.full_name
            != "registry.terraform.io/hashicorp/google"
          or ($provider_config.alias // null) != null
        )
      | {
          address: .spec.data_address,
          binary: .spec.binary,
          reason: "effective job-binary identity or Terraform wiring mismatch"
        }
    ],
    missing_or_duplicate_job_binary_jobs: [
      $job_binary_specs[] as $spec
      | (
          [
            $changes[]
            | select(
                .type == "nomad_job"
                and .address == $spec.job_address
              )
          ]
          | length
        ) as $count
      | select(
          (
            $spec.required_job == true
            and $count != 1
          )
          or (
            $spec.required_job != true
            and $count > 1
          )
        )
      | {
          address: $spec.job_address,
          binary: $spec.binary,
          required: $spec.required_job,
          count: $count
        }
    ],
    invalid_job_binary_jobs: [
      $job_binary_specs[] as $spec
      | $changes[]
      | select(
          .type == "nomad_job"
          and .address == $spec.job_address
        )
      | . as $job
      | (
          if ($job.change.after.jobspec | type) == "string" then
            [
              $job.change.after.jobspec
              | scan(
                  "(?m)^[[:space:]]*source[[:space:]]*=[[:space:]]*\"(gcs::https://www.googleapis.com/storage/v1/[^\"]+)\""
                )
              | .[0]
            ]
            | sort
          else
            []
          end
        ) as $actual_sources
      | (
          [
            $artifacts.job_binaries[$spec.binary].nomad_source
          ]
        ) as $expected_sources
      | (
          if $spec.allow_unknown_jobspec == true then
            (
              $nomad_config.module.module_calls[$spec.module_name]? // {}
            ) as $module_call
            | (
                $module_call.source == $spec.module_source
                and (
                  $module_call.expressions.artifact_source.references
                  // []
                ) == [$spec.artifact_source_reference]
              )
          else
            false
          end
        ) as $unknown_source_wiring_valid
      | select(
          if ($job.change.after.jobspec | type) == "string" then
            $actual_sources != $expected_sources
          else
            (
              ($job.change.after_unknown.jobspec // false) != true
              or $unknown_source_wiring_valid != true
            )
          end
        )
      | {
          address: $spec.job_address,
          binary: $spec.binary,
          expected_sources: $expected_sources,
          actual_sources: $actual_sources,
          jobspec_unknown: (
            ($job.change.after.jobspec | type) != "string"
          ),
          unknown_source_wiring_valid: $unknown_source_wiring_valid
        }
    ]
  }
