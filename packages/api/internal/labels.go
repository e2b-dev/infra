package internal

func MakeVolumeTypeLabel(volumeType string) string {
	return "persistent-volume-type=" + volumeType
}
