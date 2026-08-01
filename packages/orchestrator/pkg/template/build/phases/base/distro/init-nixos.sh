# NixOS is declaratively configured; drop the baked systemd units so
# activation can own /etc/systemd/system as a store symlink (foreign files
# there make systemd boot with no units at all).
echo "NixOS is declaratively configured; removing the baked systemd drop-ins"
rm -rf /etc/systemd/system
