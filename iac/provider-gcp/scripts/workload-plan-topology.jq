def managed_changes:
  [
    .resource_changes[]?
    | select(.mode == "managed")
  ];

def prior_state_resources:
  [
    .prior_state.values.root_module?
    | select(. != null)
    | recurse(.child_modules[]?)
    | .resources[]?
  ];

def normalize_compute_resource_id:
  if type == "string" then
    sub("^https://www.googleapis.com/compute/v1/"; "")
  else
    .
  end;

def is_mig:
  .type == "google_compute_instance_group_manager"
  or .type == "google_compute_region_instance_group_manager";

def mig_role:
  if .type == "google_compute_region_instance_group_manager"
    and (.address == "module.cluster.google_compute_region_instance_group_manager.server_pool") then
    "server"
  elif .type == "google_compute_instance_group_manager"
    and (.address == "module.cluster.google_compute_instance_group_manager.api_pool") then
    "api"
  elif .type == "google_compute_instance_group_manager"
    and (.address == "module.cluster.google_compute_instance_group_manager.clickhouse_pool") then
    "clickhouse"
  elif .type == "google_compute_instance_group_manager"
    and (.address == "module.cluster.google_compute_instance_group_manager.loki_pool") then
    "loki"
  elif .type == "google_compute_region_instance_group_manager"
    and (
      .address
      | test(
          "^module\\.cluster\\.module\\.build_cluster\\[\"[^\"]+\"\\]\\.google_compute_region_instance_group_manager\\.pool$"
        )
    ) then
    "build"
  elif .type == "google_compute_region_instance_group_manager"
    and (
      .address
      | test(
          "^module\\.cluster\\.module\\.client_cluster\\[\"[^\"]+\"\\]\\.google_compute_region_instance_group_manager\\.pool$"
        )
    ) then
    "client"
  else
    null
  end;

def template_role:
  if .type != "google_compute_instance_template" then
    null
  elif .address == "module.cluster.google_compute_instance_template.server" then
    "server"
  elif .address == "module.cluster.google_compute_instance_template.api" then
    "api"
  elif .address == "module.cluster.google_compute_instance_template.clickhouse" then
    "clickhouse"
  elif .address == "module.cluster.google_compute_instance_template.loki" then
    "loki"
  elif (
    .address
    | test(
        "^module\\.cluster\\.module\\.build_cluster\\[\"[^\"]+\"\\]\\.google_compute_instance_template\\.template$"
      )
  ) then
    "build"
  elif (
    .address
    | test(
        "^module\\.cluster\\.module\\.client_cluster\\[\"[^\"]+\"\\]\\.google_compute_instance_template\\.template$"
      )
  ) then
    "client"
  else
    null
  end;

def autoscaler_address($address):
  $address
  | sub(
      "\\.google_compute_region_instance_group_manager\\.pool$";
      ".google_compute_region_autoscaler.autoscaler[0]"
    );

def unknown_field($resource; $field):
  any(
    ($resource.change.after_unknown // {} | .. | objects);
    has($field) and .[$field] == true
  );

def unknown_child_field($resource; $container; $field):
  any(
    (
      $resource.change.after_unknown[$container]
      // []
      | ..
      | objects
    );
    has($field) and .[$field] == true
  );

def capacity($resource; $changes):
  if unknown_field($resource; "target_size") then
    null
  elif ($resource.change.after.target_size | type) == "number" then
    $resource.change.after.target_size
  else
    autoscaler_address($resource.address) as $autoscaler_address
    | (
        [
          $changes[]
          | select(.address == $autoscaler_address)
          | select((.change.after_unknown.autoscaling_policy // false) != true)
          | select(unknown_field(.; "max_replicas") | not)
          | .change.after.autoscaling_policy[0].max_replicas
        ][0] // null
      )
  end;

def previous_capacity($resource; $changes):
  if ($resource.change.before.target_size | type) == "number" then
    $resource.change.before.target_size
  else
    autoscaler_address($resource.address) as $autoscaler_address
    | (
        [
          $changes[]
          | select(.address == $autoscaler_address)
          | .change.before.autoscaling_policy[0].max_replicas
          | select(type == "number")
        ][0] // null
      )
  end;

def machine_vcpus($machine_type):
  try (
    $machine_type
    | capture("^(?:e2|n1)-standard-(?<vcpus>[1-9][0-9]*)$")
    | .vcpus
    | tonumber
  ) catch null;

def disk_usage($resource):
  reduce ($resource.change.after.disk // [])[] as $disk (
    {
      pd_ssd_gb: 0,
      pd_standard_gb: 0,
      local_ssd_gb: 0,
      invalid: []
    };
    if (
      ($disk.disk_size_gb | type) != "number"
      or $disk.disk_size_gb <= 0
      or ($disk.disk_size_gb | floor) != $disk.disk_size_gb
    ) then
      .invalid += [
        {
          disk_type: ($disk.disk_type // null),
          disk_size_gb: ($disk.disk_size_gb // null),
          reason: "invalid-size"
        }
      ]
    elif (
      ($disk.disk_type == "pd-ssd" or $disk.disk_type == "pd-balanced")
      and (($disk.type // "PERSISTENT") == "PERSISTENT")
    ) then
      .pd_ssd_gb += $disk.disk_size_gb
    elif (
      $disk.disk_type == "pd-standard"
      and (($disk.type // "PERSISTENT") == "PERSISTENT")
    ) then
      .pd_standard_gb += $disk.disk_size_gb
    elif $disk.disk_type == "local-ssd" and $disk.type == "SCRATCH" then
      .local_ssd_gb += $disk.disk_size_gb
    else
      .invalid += [
        {
          disk_type: ($disk.disk_type // null),
          disk_size_gb: $disk.disk_size_gb,
          disk_resource_type: ($disk.type // null),
          reason: "unsupported-type"
        }
      ]
    end
  );

def number_or_zero($value):
  if ($value | type) == "number" then $value else 0 end;

def usage_zero:
  {
    instances: 0,
    global_vcpus: 0,
    regional_cpus: 0,
    pd_ssd_gb: 0,
    pd_standard_gb: 0,
    local_ssd_gb: 0,
    regional_public_ips: 0
  };

def add_usage($left; $right):
  reduce (usage_zero | keys[]) as $key (
    {};
    .[$key] = (
      number_or_zero($left[$key])
      + number_or_zero($right[$key])
    )
  );

def max_usage($left; $right):
  reduce (usage_zero | keys[]) as $key (
    {};
    .[$key] = (
      [
        number_or_zero($left[$key]),
        number_or_zero($right[$key])
      ]
      | max
    )
  );

def role_template($templates; $role):
  (
    [
      $templates[]
      | select(.role == $role)
    ][0]
    // {
      vcpus: 0,
      pd_ssd_gb: 0,
      pd_standard_gb: 0,
      local_ssd_gb: 0,
      regional_public_ip: false
    }
  );

def scaled_role_usage($templates; $role; $count):
  role_template($templates; $role) as $template
  | {
      instances: number_or_zero($count),
      global_vcpus: (
        number_or_zero($template.vcpus)
        * number_or_zero($count)
      ),
      regional_cpus: (
        number_or_zero($template.vcpus)
        * number_or_zero($count)
      ),
      pd_ssd_gb: (
        number_or_zero($template.pd_ssd_gb)
        * number_or_zero($count)
      ),
      pd_standard_gb: (
        number_or_zero($template.pd_standard_gb)
        * number_or_zero($count)
      ),
      local_ssd_gb: (
        number_or_zero($template.local_ssd_gb)
        * number_or_zero($count)
      ),
      regional_public_ips: (
        if $template.regional_public_ip == true then
          number_or_zero($count)
        else
          0
        end
      )
    };

def reserve_usage($reserve):
  {
    instances: $reserve.instances,
    global_vcpus: $reserve.vcpus,
    regional_cpus: $reserve.vcpus,
    pd_ssd_gb: $reserve.pd_ssd_gb,
    pd_standard_gb: $reserve.pd_standard_gb,
    local_ssd_gb: $reserve.local_ssd_gb,
    regional_public_ips: $reserve.regional_public_ips
  };

def expected_cloud_sql_address($resource; $policy):
  ($policy.resource_addresses | index($resource.address)) != null;

def is_cloud_sql_resource($resource; $policy):
  expected_cloud_sql_address($resource; $policy)
  or $resource.type == "google_sql_database_instance"
  or $resource.type == "google_sql_database"
  or $resource.type == "google_sql_user"
  or $resource.type == "google_service_networking_connection"
  or (
    $resource.type == "google_compute_global_address"
    and (
      ($resource.change.after.purpose // null) == "VPC_PEERING"
      or ($resource.change.before.purpose // null) == "VPC_PEERING"
    )
  );

def is_safe_setup_object_replacement:
  .type == "google_storage_bucket_object"
  and (
    .address
    | test(
        "^module\\.cluster\\.google_storage_bucket_object\\.setup_config_objects\\[\\\"scripts/(?:configure-docker-gcp|run-consul|run-nomad)\\.sh\\\"\\]$"
      )
  )
  and .change.actions == ["create", "delete"]
  and .change.after.deletion_policy == "ABANDON"
  and (.change.before.bucket | type) == "string"
  and .change.after.bucket == .change.before.bucket
  and (
    .change.before.name
    | test("^(?:configure-docker-gcp|run-consul|run-nomad)-[0-9a-f]{5}\\.sh$")
  )
  and (
    .change.after.name
    | test("^(?:configure-docker-gcp|run-consul|run-nomad)-[0-9a-f]{5}\\.sh$")
  )
  and .change.after.name != .change.before.name;

managed_changes as $changes
| (
    [
      $changes[]
      | select(.type == "google_compute_address")
      | select(
          .address as $address
          | (
              $expected.fixed_regional_public_ip_addresses
              | index($address)
            ) != null
        )
    ]
  ) as $fixed_regional_public_ip_resources
| (
    $fixed_regional_public_ip_resources
    | map(.address)
    | sort
  ) as $fixed_regional_public_ip_addresses
| (
    [
      $changes[]
      | select(is_mig)
    ]
  ) as $migs
| (
    [
      $changes[]
      | select(.type == "google_compute_instance_template")
    ]
  ) as $instance_templates
| (
    [
      $instance_templates[] as $resource
      | ($resource | template_role) as $role
      | select($role != null)
      | disk_usage($resource) as $disk_usage
      | ($resource.change.after.network_interface // []) as $network_interfaces
      | {
          address: $resource.address,
          role: $role,
          machine_type: ($resource.change.after.machine_type // null),
          vcpus: machine_vcpus($resource.change.after.machine_type),
          pd_ssd_gb: $disk_usage.pd_ssd_gb,
          pd_standard_gb: $disk_usage.pd_standard_gb,
          local_ssd_gb: $disk_usage.local_ssd_gb,
          regional_public_ip: (
            if (
              ($network_interfaces | length) == 1
              and ($network_interfaces[0].access_config | type) == "array"
            ) then
              ($network_interfaces[0].access_config | length) == 1
            else
              null
            end
          ),
          invalid_disks: $disk_usage.invalid,
          unresolved: (
            unknown_field($resource; "machine_type")
            or machine_vcpus($resource.change.after.machine_type) == null
            or unknown_field($resource; "disk")
            or unknown_child_field($resource; "disk"; "disk_size_gb")
            or unknown_child_field($resource; "disk"; "disk_type")
            or unknown_field($resource; "network_interface")
            or unknown_child_field(
              $resource;
              "network_interface";
              "access_config"
            )
            or (($resource.change.after.disk // []) | length) == 0
            or ($network_interfaces | length) != 1
            or ($network_interfaces[0].access_config | type) != "array"
            or ($network_interfaces[0].access_config | length) > 1
          )
        }
    ]
  ) as $templates
| (
    [
      $migs[] as $resource
      | ($resource | mig_role) as $role
      | select($role != null)
      | capacity($resource; $changes) as $capacity
      | {
          address: $resource.address,
          role: $role,
          actions: $resource.change.actions,
          capacity: $capacity,
          previous_capacity: previous_capacity($resource; $changes),
          regional: (
            $resource.type
            == "google_compute_region_instance_group_manager"
          ),
          distribution_policy_zones: (
            $resource.change.after.distribution_policy_zones
            // null
          ),
          distribution_policy_zones_unknown: (
            unknown_field($resource; "distribution_policy_zones")
          ),
          surge: (
            if $capacity == 0 then
              0
            else
              ($resource.change.after.update_policy[0].max_surge_fixed // 0)
            end
          ),
          surge_percent: (
            if $capacity == 0 then
              0
            else
              ($resource.change.after.update_policy[0].max_surge_percent // 0)
            end
          ),
          max_unavailable: (
            if $capacity == 0 then
              0
            else
              (
                $resource.change.after.update_policy[0].max_unavailable_fixed
                // 0
              )
            end
          ),
          surge_unknown: (
            ($resource.change.after_unknown.update_policy // false) == true
            or unknown_field($resource; "max_surge_fixed")
            or unknown_field($resource; "max_surge_percent")
          ),
          max_unavailable_unknown: (
            ($resource.change.after_unknown.update_policy // false) == true
            or unknown_field($resource; "max_unavailable_fixed")
          )
        }
    ]
  ) as $rows
| (
    reduce ($expected.expected_role_max_instances | keys[]) as $role (
      {};
      .[$role] = (
        [
          $rows[]
          | select(.role == $role)
          | .capacity
          | select(type == "number")
        ]
        | add // 0
      )
    )
  ) as $role_max_instances
| (
    reduce ($expected.expected_role_surge_instances | keys[]) as $role (
      {};
      .[$role] = (
        [
          $rows[]
          | select(.role == $role)
          | .surge
          | select(type == "number")
        ]
        | add // 0
      )
    )
  ) as $role_surge_instances
| (
    reduce (
      $expected.expected_role_max_unavailable_instances
      | keys[]
    ) as $role (
      {};
      .[$role] = (
        [
          $rows[]
          | select(.role == $role)
          | .max_unavailable
          | select(type == "number")
        ]
        | add // 0
      )
    )
  ) as $role_max_unavailable_instances
| (
    reduce ($expected.expected_role_resources | keys[]) as $role (
      {};
      .[$role] = (
        [
          $templates[]
          | select(.role == $role)
          | {
              machine_type,
              vcpus,
              pd_ssd_gb,
              pd_standard_gb,
              local_ssd_gb,
              regional_public_ip
            }
        ][0] // null
      )
    )
  ) as $role_resources
| (
    add_usage(
      reduce $rows[] as $row (
        usage_zero;
        add_usage(
          .;
          scaled_role_usage(
            $templates;
            $row.role;
            number_or_zero($row.capacity)
          )
        )
      );
      {
        regional_public_ips: (
          $fixed_regional_public_ip_resources | length
        )
      }
    )
  ) as $base_usage
| (
    reduce $rows[] as $row (
      usage_zero;
      add_usage(
        .;
        scaled_role_usage(
          $templates;
          $row.role;
          number_or_zero($row.surge)
        )
      )
    )
  ) as $surge_usage
| add_usage($base_usage; $surge_usage) as $rollout_usage
| add_usage(
    $base_usage;
    reserve_usage($expected.transient_reserve)
  ) as $packer_usage
| max_usage($rollout_usage; $packer_usage) as $peak_usage
| (
    [
      $changes[] as $resource
      | select(
          is_cloud_sql_resource(
            $resource;
            $expected.expected_cloud_sql
          )
        )
      | $resource
    ]
  ) as $cloud_sql_resources
| (
    $cloud_sql_resources
    | map(select(.address == "google_sql_database_instance.operator_canary"))
    | first
  ) as $cloud_sql_instance
| (
    $cloud_sql_resources
    | map(select(.address == "google_compute_global_address.cloud_sql_private_services"))
    | first
  ) as $cloud_sql_private_services_range
| (
    $cloud_sql_resources
    | map(select(.address == "google_service_networking_connection.cloud_sql"))
    | first
  ) as $cloud_sql_private_services_connection
| (
    $cloud_sql_resources
    | map(select(.address == "google_project_service_identity.cloud_sql"))
    | first
  ) as $cloud_sql_service_identity
| (
    $cloud_sql_resources
    | map(select(.address == "google_project_service_identity.service_networking"))
    | first
  ) as $service_networking_service_identity
| (
    $cloud_sql_resources
    | map(select(.address == "random_password.cloud_sql_operator_canary"))
    | first
  ) as $cloud_sql_password
| (
    $cloud_sql_resources
    | map(select(.address == "google_sql_database.operator_canary"))
    | first
  ) as $cloud_sql_database
| (
    $cloud_sql_resources
    | map(select(.address == "google_sql_user.operator_canary"))
    | first
  ) as $cloud_sql_user
| (
    [
      $changes[]
      | select(
          .address
          == "module.init.google_secret_manager_secret.postgres_connection_string"
        )
    ]
  ) as $cloud_sql_connection_secret_containers
| (
    $cloud_sql_connection_secret_containers
    | first
  ) as $cloud_sql_connection_secret_container
| (
    prior_state_resources
    | map(
        select(
          .address == "module.init.data.google_project.current"
          and .mode == "data"
        )
      )
  ) as $cloud_sql_project_states
| (
    $cloud_sql_project_states
    | first
  ) as $cloud_sql_project_state
| (
    [
      .resource_changes[]?
      | select(
          .address == "module.init.data.google_project.current"
        )
    ]
  ) as $cloud_sql_project_changes
| (
    $cloud_sql_project_changes
    | first
  ) as $cloud_sql_project_change
| (
    if ($cloud_sql_project_changes | length) == 1
    then $cloud_sql_project_change.change.after
    else $cloud_sql_project_state.values
    end
  ) as $cloud_sql_project_identity
| {
    role_max_instances: $role_max_instances,
    role_surge_instances: $role_surge_instances,
    role_max_unavailable_instances: $role_max_unavailable_instances,
    role_resources: $role_resources,
    fixed_regional_public_ip_addresses: $fixed_regional_public_ip_addresses,
    base_usage: $base_usage,
    rollout_usage: $rollout_usage,
    packer_usage: $packer_usage,
    peak_usage: $peak_usage,
    destructive_migs: [
      $migs[]
      | select(.change.actions | index("delete"))
      | .address
    ],
    unknown_migs: [
      $migs[]
      | select(mig_role == null)
      | .address
    ],
    unknown_templates: [
      $instance_templates[]
      | select(template_role == null)
      | .address
    ],
    unexpected_quota_resources: [
      $changes[]
      | select(
          .type == "google_compute_instance"
          or .type == "google_compute_disk"
          or .type == "google_compute_region_disk"
          or .type == "google_compute_address"
          or .type == "google_compute_region_address"
          or .type == "google_compute_region_autoscaler"
        )
      | select(
          .address as $address
          | (
              $expected.fixed_regional_public_ip_addresses
              | index($address)
            ) == null
        )
      | {
          address,
          type
        }
    ],
    invalid_fixed_regional_public_ip_resources: [
      $fixed_regional_public_ip_resources[]
      | select(
          (.change.actions | index("delete")) != null
          or .change.after.address_type != "EXTERNAL"
          or (.change.after.region | type) != "string"
          or (
            .change.after.region != $expected.gcp_region
            and (
              .change.after.region
              | endswith("/regions/" + $expected.gcp_region)
              | not
            )
          )
        )
      | {
          address,
          actions: .change.actions,
          address_type: .change.after.address_type,
          region: .change.after.region
        }
    ],
    missing_or_duplicate_mig_roles: [
      $expected.expected_role_max_instances
      | keys[] as $role
      | (
          [
            $rows[]
            | select(.role == $role)
          ]
          | length
        ) as $count
      | select($count != 1)
      | {
          role: $role,
          count: $count
        }
    ],
    missing_or_duplicate_template_roles: [
      $expected.expected_role_resources
      | keys[] as $role
      | (
          [
            $templates[]
            | select(.role == $role)
          ]
          | length
        ) as $count
      | select($count != 1)
      | {
          role: $role,
          count: $count
        }
    ],
    unresolved_capacities: [
      $rows[]
      | select((.capacity | type) != "number")
      | .address
    ],
    unresolved_previous_capacities: [
      $rows[]
      | select(.actions | index("create") | not)
      | select((.previous_capacity | type) != "number")
      | .address
    ],
    capacity_reductions: [
      $rows[]
      | select((.previous_capacity | type) == "number")
      | select((.capacity | type) == "number")
      | select(.capacity < .previous_capacity)
      | {
          address,
          before: .previous_capacity,
          after: .capacity
        }
    ],
    unresolved_surges: [
      $rows[]
      | select(.capacity != 0 and .surge_unknown)
      | .address
    ],
    unresolved_max_unavailable: [
      $rows[]
      | select(.capacity != 0 and .max_unavailable_unknown)
      | .address
    ],
    invalid_surges: [
      $rows[]
      | select(
          (.surge | type) != "number"
          or .surge < 0
          or (.surge | floor) != .surge
        )
      | {
          address,
          surge
        }
    ],
    percentage_surges: [
      $rows[]
      | select(.surge_percent != 0)
      | .address
    ],
    invalid_max_unavailable: [
      $rows[]
      | select(
          (.max_unavailable | type) != "number"
          or .max_unavailable < 0
          or (.max_unavailable | floor) != .max_unavailable
        )
      | {
          address,
          max_unavailable
        }
    ],
    invalid_single_unavailable_regional_migs: [
      $rows[]
      | select(.regional and .capacity != 0)
      | select(.max_unavailable == 1)
      | select(
          .distribution_policy_zones_unknown
          or (
            .distribution_policy_zones
            | type
          ) != "array"
          or (
            .distribution_policy_zones
            | length
          ) != 1
        )
      | {
          address,
          distribution_policy_zones
        }
    ],
    automated_worker_server_surges: [
      $rows[]
      | select(
          .role == "build"
          or .role == "client"
          or .role == "server"
        )
      | select(
          (.surge | type) != "number"
          or (
            .surge
            > $expected.max_automated_worker_server_surge_per_pool
          )
        )
      | {
          address,
          role,
          surge
        }
    ],
    unresolved_templates: [
      $templates[]
      | select(.unresolved)
      | .address
    ],
    invalid_template_disks: [
      $templates[]
      | select((.invalid_disks | length) != 0)
      | {
          address,
          disks: .invalid_disks
        }
    ],
    destructive_cloud_sql_resources: [
      $cloud_sql_resources[]
      | select(.change.actions | index("delete"))
      | .address
    ],
    destructive_managed_resources: [
      $changes[]
      | select(
          (
            .change.actions == ["no-op"]
            or .change.actions == ["create"]
            or .change.actions == ["read"]
            or .change.actions == ["update"]
            or (
              .type == "google_compute_instance_template"
              and template_role != null
              and .change.actions == ["create", "delete"]
            )
            or is_safe_setup_object_replacement
            or (
              .address == "module.nomad.module.orchestrator[0].random_id.orchestrator_job"
              and .type == "random_id"
              and .change.actions == ["delete", "create"]
            )
          )
          | not
        )
      | {
          address,
          type,
          actions: .change.actions
        }
    ],
    unknown_cloud_sql_resources: [
      $cloud_sql_resources[] as $resource
      | select(
          expected_cloud_sql_address(
            $resource;
            $expected.expected_cloud_sql
          )
          | not
        )
      | {
          address: $resource.address,
          type: $resource.type
        }
    ],
    missing_or_duplicate_cloud_sql_resources: [
      $expected.expected_cloud_sql.resource_addresses[] as $address
      | (
          [
            $cloud_sql_resources[]
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
    invalid_cloud_sql_resources: (
      (
        [
          {
            address: "module.init.data.google_project.current",
            count: ($cloud_sql_project_states | length),
            reason: "connection-secret-project-state-count"
          }
          | select(.count != 1)
        ]
        + [
          {
            address: "module.init.data.google_project.current",
            count: ($cloud_sql_project_changes | length),
            mode: ($cloud_sql_project_change.mode // null),
            actions: ($cloud_sql_project_change.change.actions // null),
            reason: "connection-secret-project-action"
          }
          | select(
              .count > 1
              or (
                .count == 1
                and (
                  .mode != "data"
                  or $cloud_sql_project_change.type != "google_project"
                  or (
                    .actions != ["read"]
                    and .actions != ["no-op"]
                  )
                  or $cloud_sql_project_change.change.after_unknown.project_id
                    == true
                  or $cloud_sql_project_change.change.after_unknown.number
                    == true
                )
              )
            )
        ]
        + [
          $cloud_sql_project_identity
          | select(
              ($cloud_sql_project_states | length) == 1
              and ($cloud_sql_project_changes | length) <= 1
          )
          | select(
              $cloud_sql_project_state.type != "google_project"
              or ($cloud_sql_project_state.values.project_id | type)
                != "string"
              or ($cloud_sql_project_state.values.project_id | length) == 0
              or ($cloud_sql_project_state.values.number | type)
                != "string"
              or (
                try (
                  $cloud_sql_project_state.values.number
                  | test("^[1-9][0-9]*$")
                ) catch false
                | not
              )
              or (.project_id | type) != "string"
              or (.project_id | length) == 0
              or (.number | type) != "string"
              or (
                try (.number | test("^[1-9][0-9]*$")) catch false
                | not
              )
              or .project_id
                != $cloud_sql_project_state.values.project_id
              or .number != $cloud_sql_project_state.values.number
              or .project_id != $cloud_sql_instance.change.after.project
            )
          | {
              address: "module.init.data.google_project.current",
              reason: "connection-secret-project-identity"
            }
        ]
        + [
          {
            address: "module.init.google_secret_manager_secret.postgres_connection_string",
            count: ($cloud_sql_connection_secret_containers | length),
            reason: "connection-secret-container-count"
          }
          | select(.count != 1)
        ]
        + [
          $cloud_sql_connection_secret_container
          | select(
              ($cloud_sql_connection_secret_containers | length) == 1
            )
          | (
              $cloud_sql_instance.change.after.name
              | rtrimstr(
                  $expected.expected_cloud_sql.instance_name_suffix
                )
            ) as $prefix
          | (
              "projects/"
              + $cloud_sql_instance.change.after.project
              + "/secrets/"
              + $prefix
              + $expected.expected_cloud_sql.connection_secret_id_suffix
            ) as $expected_id
          | select(
              (.change.after.project | type) != "string"
              or .change.after.project
                != $cloud_sql_instance.change.after.project
              or (.change.after.secret_id | type) != "string"
              or .change.after.secret_id
                != (
                  $prefix
                  + $expected.expected_cloud_sql.connection_secret_id_suffix
                )
              or (.change.after.id | type) != "string"
              or .change.after.id != $expected_id
              or (.change.after.name | type) != "string"
              or (.change.after.name | length) == 0
              or .change.actions != ["no-op"]
              or .change.after_unknown.project == true
              or .change.after_unknown.secret_id == true
              or .change.after_unknown.id == true
              or .change.after_unknown.name == true
              or (
                .change.after.name
                != (
                  "projects/"
                  + $cloud_sql_project_identity.number
                  + "/secrets/"
                  + $prefix
                  + $expected.expected_cloud_sql.connection_secret_id_suffix
                )
              )
            )
          | {
              address: .address,
              reason: "connection-secret-container-identity"
            }
        ]
      )
      + [
        $cloud_sql_resources[]
        | select(
            .address
            == "google_sql_database_instance.operator_canary"
          )
        | . as $resource
        | ($resource.change.after.settings[0] // {}) as $settings
        | (
            $settings.backup_configuration[0]
            // {}
          ) as $backup
        | (
            $backup.backup_retention_settings[0]
            // {}
          ) as $retention
        | (
            $settings.ip_configuration[0]
            // {}
          ) as $ip
        | select(
            $resource.change.after.database_version
              != $expected.expected_cloud_sql.database_version
            or (
              $resource.change.after.name
              | type
            ) != "string"
            or (
              try (
                $resource.change.after.name
                | endswith($expected.expected_cloud_sql.instance_name_suffix)
              ) catch false
              | not
            )
            or (
              $resource.change.after.project
              | type
            ) != "string"
            or (
              $resource.change.after.project
              | length
            ) == 0
            or (
              $resource.change.after.region
              | type
            ) != "string"
            or (
              $resource.change.after.region
              | length
            ) == 0
            or $resource.change.after.deletion_protection != true
            or $settings.tier
              != $expected.expected_cloud_sql.tier
            or $settings.edition
              != $expected.expected_cloud_sql.edition
            or $settings.availability_type
              != $expected.expected_cloud_sql.availability_type
            or $settings.disk_type
              != $expected.expected_cloud_sql.disk_type
            or $settings.disk_size
              != $expected.expected_cloud_sql.disk_size_gb
            or $settings.disk_autoresize != true
            or $settings.disk_autoresize_limit
              != $expected.expected_cloud_sql.disk_autoresize_limit_gb
            or $settings.deletion_protection_enabled != true
            or $backup.enabled != true
            or $backup.location != $resource.change.after.region
            or $backup.start_time
              != $expected.expected_cloud_sql.backup_start_time
            or $backup.point_in_time_recovery_enabled != true
            or $backup.transaction_log_retention_days
              != $expected.expected_cloud_sql.transaction_log_retention_days
            or $retention.retained_backups
              != $expected.expected_cloud_sql.retained_backups
            or $retention.retention_unit != "COUNT"
            or $ip.ipv4_enabled != false
            or (
              $ip.private_network
              | type
            ) != "string"
            or (
              $ip.private_network
              | length
            ) == 0
            or ($ip.private_network | normalize_compute_resource_id)
              != (
                $cloud_sql_private_services_range.change.after.network
                | normalize_compute_resource_id
              )
            or ($ip.private_network | normalize_compute_resource_id)
              != (
                $cloud_sql_private_services_connection.change.after.network
                | normalize_compute_resource_id
              )
            or (
              $ip.allocated_ip_range
              | type
            ) != "string"
            or (
              $ip.allocated_ip_range
              | length
            ) == 0
            or $ip.allocated_ip_range
              != $cloud_sql_private_services_range.change.after.name
            or $ip.allocated_ip_range
              != $cloud_sql_private_services_connection.change.after.reserved_peering_ranges[0]
            or $ip.enable_private_path_for_google_cloud_services != false
            or $ip.ssl_mode != $expected.expected_cloud_sql.ssl_mode
          )
        | {
            address: $resource.address,
            reason: "instance-settings"
          }
      ]
      + [
        $cloud_sql_resources[]
        | select(
            .address
            == "google_compute_global_address.cloud_sql_private_services"
          )
        | select(
            .change.after.purpose != "VPC_PEERING"
            or .change.after.address_type != "INTERNAL"
            or .change.after.project != $cloud_sql_instance.change.after.project
            or (
              .change.after.name
              | type
            ) != "string"
            or (
              .change.after.name
              | length
            ) == 0
            or .change.after.prefix_length
              != $expected.expected_cloud_sql.private_services_prefix_length
            or (
              .change.after.network
              | type
            ) != "string"
            or (
              .change.after.network
              | length
            ) == 0
            or (.change.after.network | normalize_compute_resource_id)
              != (
                $cloud_sql_instance.change.after.settings[0].ip_configuration[0].private_network
                | normalize_compute_resource_id
              )
            or (.change.after.network | normalize_compute_resource_id)
              != (
                $cloud_sql_private_services_connection.change.after.network
                | normalize_compute_resource_id
              )
          )
        | {
            address: .address,
            reason: "private-services-range"
          }
      ]
      + [
        $cloud_sql_resources[]
        | select(
            .address
            == "google_service_networking_connection.cloud_sql"
          )
        | select(
            .change.after.service != "servicenetworking.googleapis.com"
            or .change.after.deletion_policy != "ABANDON"
            or (
              .change.after.network
              | type
            ) != "string"
            or (
              .change.after.network
              | length
            ) == 0
            or (.change.after.network | normalize_compute_resource_id)
              != (
                $cloud_sql_private_services_range.change.after.network
                | normalize_compute_resource_id
              )
            or (.change.after.network | normalize_compute_resource_id)
              != (
                $cloud_sql_instance.change.after.settings[0].ip_configuration[0].private_network
                | normalize_compute_resource_id
              )
            or (
              .change.after.reserved_peering_ranges
              | type
            ) != "array"
            or (
              .change.after.reserved_peering_ranges
              | length
            ) != 1
            or .change.after.reserved_peering_ranges[0]
              != $cloud_sql_private_services_range.change.after.name
            or .change.after.reserved_peering_ranges[0]
              != $cloud_sql_instance.change.after.settings[0].ip_configuration[0].allocated_ip_range
          )
        | {
            address: .address,
            reason: "private-services-connection"
          }
      ]
      + [
        $cloud_sql_resources[]
        | select(
            .address
            == "google_sql_database.operator_canary"
          )
        | select(
            .change.after.name
              != $expected.expected_cloud_sql.database_name
            or .change.after.project != $cloud_sql_instance.change.after.project
            or (
              .change.after.instance
              | type
            ) != "string"
            or (
              .change.after.instance
              | length
            ) == 0
            or .change.after.instance != $cloud_sql_instance.change.after.name
          )
        | {
            address: .address,
            reason: "database"
          }
      ]
      + [
        $cloud_sql_resources[]
        | select(
            .address
            == "google_sql_user.operator_canary"
          )
        | select(
            .change.after.name
              != $expected.expected_cloud_sql.user_name
            or .change.after.project != $cloud_sql_instance.change.after.project
            or (
              .change.after.instance
              | type
            ) != "string"
            or (
              .change.after.instance
              | length
            ) == 0
            or .change.after.instance != $cloud_sql_instance.change.after.name
            or .change.after_sensitive.password != true
            or (
              if (
                (.change.after.password | type) == "string"
                and ($cloud_sql_password.change.after.result | type) == "string"
              ) then
                .change.after.password != $cloud_sql_password.change.after.result
              elif (
                .change.after.password == null
                and $cloud_sql_password.change.after.result == null
              ) then
                .change.after_unknown.password != true
                  or $cloud_sql_password.change.after_unknown.result != true
              else
                true
              end
            )
          )
        | {
            address: .address,
            reason: "database-user"
          }
      ]
      + [
        $cloud_sql_resources[]
        | select(
            .address
            == "random_password.cloud_sql_operator_canary"
          )
        | select(
            .change.after.length != 32
            or .change.after.special != false
            or .change.after_sensitive.result != true
            or (
              if (.change.after.result | type) == "string" then
                false
              else
                .change.after.result != null
                  or .change.after_unknown.result != true
              end
            )
          )
        | {
            address: .address,
            reason: "database-password-policy"
          }
      ]
      + [
        $cloud_sql_resources[]
        | select(
            .address
            == "google_secret_manager_secret_version.postgres_connection_string"
          )
        | select(
            ($cloud_sql_connection_secret_containers | length) != 1
            or (.change.after.secret | type) != "string"
            or .change.after.secret
              != $cloud_sql_connection_secret_container.change.after.name
            or .change.after_unknown.secret == true
            or .change.after_sensitive.secret_data != true
            or (
              if (.change.after.secret_data | type) == "string" then
                if .change.after_unknown.secret_data == true then
                  true
                elif (
                  ($cloud_sql_user.change.after.name | type) == "string"
                  and ($cloud_sql_password.change.after.result | type) == "string"
                  and (
                    $cloud_sql_instance.change.after.private_ip_address
                    | type
                  ) == "string"
                  and ($cloud_sql_database.change.after.name | type) == "string"
                ) then
                  .change.after.secret_data
                    != (
                      "postgresql://"
                      + $cloud_sql_user.change.after.name
                      + ":"
                      + $cloud_sql_password.change.after.result
                      + "@"
                      + $cloud_sql_instance.change.after.private_ip_address
                      + ":5432/"
                      + $cloud_sql_database.change.after.name
                      + "?sslmode=require"
                    )
                else
                  true
                end
              else
                .change.after.secret_data != null
                  or .change.after_unknown.secret_data != true
              end
            )
          )
        | {
            address: .address,
            reason: "connection-secret"
          }
      ]
      + [
        $cloud_sql_resources[]
        | select(
            .address
            == "terraform_data.cloud_sql_connection_budget"
          )
        | (.change.after.input // {}) as $input
        | select(
            ($input.db_max_open_connections | type) != "number"
            or $input.db_max_open_connections < 1
            or (
              $input.db_max_open_connections
              | floor
            ) != $input.db_max_open_connections
            or ($input.auth_db_max_open_connections | type) != "number"
            or $input.auth_db_max_open_connections < 1
            or (
              $input.auth_db_max_open_connections
              | floor
            ) != $input.auth_db_max_open_connections
            or ($input.db_min_idle_connections | type) != "number"
            or $input.db_min_idle_connections < 0
            or (
              $input.db_min_idle_connections
              | floor
            ) != $input.db_min_idle_connections
            or $input.db_min_idle_connections
              > $input.db_max_open_connections
            or ($input.auth_db_min_idle_connections | type) != "number"
            or $input.auth_db_min_idle_connections < 0
            or (
              $input.auth_db_min_idle_connections
              | floor
            ) != $input.auth_db_min_idle_connections
            or $input.auth_db_min_idle_connections
              > $input.auth_db_max_open_connections
            or $input.api_server_count
              != $expected.expected_cloud_sql.api_server_count
            or $input.dashboard_api_count
              != $expected.expected_cloud_sql.dashboard_api_count
            or $input.migrator_max_open_connections
              != $expected.expected_cloud_sql.migrator_max_open_connections
            or $input.docker_reverse_proxy_max_open_connections
              != $expected.expected_cloud_sql.docker_reverse_proxy_max_open_connections
            or $input.dashboard_api_max_open_connections_per_instance
              != $expected.expected_cloud_sql.dashboard_api_max_open_connections_per_instance
            or $input.maximum_concurrent_connections
              != (
                (
                  $input.db_max_open_connections
                  + $input.auth_db_max_open_connections
                )
                * $input.api_server_count
                + (
                  $input.dashboard_api_max_open_connections_per_instance
                  * $input.dashboard_api_count
                )
                + $input.docker_reverse_proxy_max_open_connections
                + $input.migrator_max_open_connections
              )
            or $input.maximum_concurrent_connections
              > $expected.expected_cloud_sql.application_connection_budget
          )
        | {
            address: .address,
            reason: "application-connection-budget"
          }
      ]
      + [
        $cloud_sql_resources[]
        | select(.address == "google_project_service.cloud_sql_admin_api")
        | select(
            .change.after.service != "sqladmin.googleapis.com"
            or .change.after.project != $cloud_sql_instance.change.after.project
            or .change.after.disable_on_destroy != false
          )
        | {
            address: .address,
            reason: "cloud-sql-api"
          }
      ]
      + [
        $cloud_sql_resources[]
        | select(.address == "google_project_service.service_networking_api")
        | select(
            .change.after.service != "servicenetworking.googleapis.com"
            or .change.after.project != $cloud_sql_instance.change.after.project
            or .change.after.disable_on_destroy != false
          )
        | {
            address: .address,
            reason: "service-networking-api"
          }
      ]
      + [
        $cloud_sql_resources[]
        | select(.address == "google_project_service_identity.cloud_sql")
        | select(
            .change.after.service != "sqladmin.googleapis.com"
            or .change.after.project != $cloud_sql_instance.change.after.project
          )
        | {
            address: .address,
            reason: "cloud-sql-service-identity"
          }
      ]
      + [
        $cloud_sql_resources[]
        | select(.address == "google_project_service_identity.service_networking")
        | select(
            .change.after.service != "servicenetworking.googleapis.com"
            or .change.after.project != $cloud_sql_instance.change.after.project
          )
        | {
            address: .address,
            reason: "service-networking-identity"
          }
      ]
      + [
        $cloud_sql_resources[]
        | select(.address == "google_project_iam_member.cloud_sql_service_agent")
        | select(
            .change.after.role != "roles/cloudsql.serviceAgent"
            or .change.after.project != $cloud_sql_instance.change.after.project
            or (
              if (
                (.change.after.member | type) == "string"
                and ($cloud_sql_service_identity.change.after.email | type) == "string"
              ) then
                .change.after.member
                  != ("serviceAccount:" + $cloud_sql_service_identity.change.after.email)
              else
                .change.after_unknown.member != true
                  or $cloud_sql_service_identity.change.after_unknown.email != true
              end
            )
          )
        | {
            address: .address,
            reason: "cloud-sql-service-agent-role"
          }
      ]
      + [
        $cloud_sql_resources[]
        | select(.address == "google_project_iam_member.service_networking_service_agent")
        | select(
            .change.after.role
            != "roles/servicenetworking.serviceAgent"
            or .change.after.project != $cloud_sql_instance.change.after.project
            or (
              if (
                (.change.after.member | type) == "string"
                and ($service_networking_service_identity.change.after.email | type) == "string"
              ) then
                .change.after.member
                  != ("serviceAccount:" + $service_networking_service_identity.change.after.email)
              else
                .change.after_unknown.member != true
                  or $service_networking_service_identity.change.after_unknown.email != true
              end
            )
          )
        | {
            address: .address,
            reason: "service-networking-service-agent-role"
          }
      ]
    ),
    quota_violations: [
      $expected.quota_limits
      | keys[] as $quota
      | select($peak_usage[$quota] > $expected.quota_limits[$quota])
      | {
          quota: $quota,
          planned: $peak_usage[$quota],
          limit: $expected.quota_limits[$quota]
        }
    ]
  }
