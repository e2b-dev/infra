#!/bin/bash

set -euo pipefail

# -------------------------------------------------------------------------------------------------
# Upload envs from disk to GCS
# -------------------------------------------------------------------------------------------------
# First argument is target dir name
TARGET_DIR_NAME=$1

# Second argument is template bucket name
TEMPLATE_BUCKET_NAME=$2

# Third argument is template id that you can specify to upload a specific env
TEMPLATE_ID=$3
# -------------------------------------------------------------------------------------------------

echo "Uploading envs from ${TARGET_DIR_NAME} to GCS"

COMMAND="gcloud storage cp --verbosity error -n"

# Initialize counter for uploaded envs
uploaded_env_count=0
total_envs=$(ls ${TARGET_DIR_NAME} | wc -l)

# Record the start time
start_time=$(date +%s)

# iterate over all directories in the target dir
for template_id in $(ls ${TARGET_DIR_NAME}); do
  if [ -n "${TEMPLATE_ID}" ] && [ "${template_id}" != "${TEMPLATE_ID}" ]; then
    continue
  fi

  # Increment the counter for uploaded envs
  uploaded_env_count=$((uploaded_env_count + 1))

  echo -e "\n------------------${uploaded_env_count}/${total_envs}-----------------"
  echo "Uploading env ${template_id}"
  # Get the build id by printing the content of build_id text file, skip dir if not build_id file exists
  if [ ! -f "${TARGET_DIR_NAME}/${template_id}/build_id" ]; then
    echo "Skip ${template_id} because build_id file does not exist"
    continue
  fi

  BUILD_ID=$(cat ${TARGET_DIR_NAME}/${template_id}/build_id)
  echo "Build ID: ${BUILD_ID}"

  # Upload env to GCS via gcloud storage cp, copy only "memfile", "rootfs.ext4" and "snapfile" from the dir
  # First get and print the paths to the files
  MEMFILE_PATH=$(ls ${TARGET_DIR_NAME}/${template_id}/memfile)
  ROOTFS_EXT4_PATH=$(ls ${TARGET_DIR_NAME}/${template_id}/rootfs.ext4)
  SNAPFILE_PATH=$(ls ${TARGET_DIR_NAME}/${template_id}/snapfile)

  # Check if files exist
  if [ ! -f "${MEMFILE_PATH}" ]; then
    echo "Skip ${template_id} because memfile does not exist"
    continue
  fi

  if [ ! -f "${ROOTFS_EXT4_PATH}" ]; then
    echo "Skip ${template_id} because rootfs.ext4 does not exist"
    continue
  fi

  if [ ! -f "${SNAPFILE_PATH}" ]; then
    echo "Skip ${template_id} because snapfile does not exist"
    continue
  fi

  BUCKET_MEMFILE_PATH="gs://${TEMPLATE_BUCKET_NAME}/${BUILD_ID}/memfile"
  BUCKET_ROOTFS_EXT4_PATH="gs://${TEMPLATE_BUCKET_NAME}/${BUILD_ID}/rootfs.ext4"
  BUCKET_SNAPFILE_PATH="gs://${TEMPLATE_BUCKET_NAME}/${BUILD_ID}/snapfile"

  # Upload the files
  echo "Uploading memfile"
  ${COMMAND} ${MEMFILE_PATH} ${BUCKET_MEMFILE_PATH} &

  echo "Uploading rootfs.ext4"
  ${COMMAND} ${ROOTFS_EXT4_PATH} ${BUCKET_ROOTFS_EXT4_PATH} &

  echo "Uploading snapfile"
  ${COMMAND} ${SNAPFILE_PATH} ${BUCKET_SNAPFILE_PATH} &

  # Wait for all background jobs to finish
  wait
done

# Print the number of uploaded envs
echo "Total number of uploaded envs: ${uploaded_env_count}"

# Record the end time
end_time=$(date +%s)

# Calculate and print the elapsed time
elapsed_time=$((end_time - start_time))
echo "Total elapsed time: ${elapsed_time} seconds"
