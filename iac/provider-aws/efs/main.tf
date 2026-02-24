resource "aws_efs_file_system" "shared_cache" {
  creation_token   = "${var.prefix}shared-cache"
  encrypted        = true
  performance_mode = "generalPurpose"
  throughput_mode  = "elastic"

  tags = merge(var.tags, {
    Name = "${var.prefix}shared-cache"
  })
}

resource "aws_efs_mount_target" "shared_cache" {
  count = length(var.subnet_ids)

  file_system_id  = aws_efs_file_system.shared_cache.id
  subnet_id       = var.subnet_ids[count.index]
  security_groups = [var.efs_sg_id]
}

resource "aws_efs_access_point" "chunks_cache" {
  file_system_id = aws_efs_file_system.shared_cache.id

  posix_user {
    gid = 0
    uid = 0
  }

  root_directory {
    path = "/chunks-cache"

    creation_info {
      owner_gid   = 0
      owner_uid   = 0
      permissions = "0777"
    }
  }

  tags = merge(var.tags, {
    Name = "${var.prefix}chunks-cache"
  })
}
