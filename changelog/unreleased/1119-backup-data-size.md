# Backups show how much data they hold

Backups now record the size of the data they hold in a new `status.dataSize`
field: the bytes Restic processed, or for CSI snapshots the disk use the agent
reported when the snapshot completed. The dashboard shows that size, with the
space a backup added as a second line, and the Backups tab card is now "Total
data size". Backups taken before the upgrade have no data size: they show
"volume size" and are left out of the total, which says how many backups it
covers. [PR #1119](https://github.com/GameplanePanel/Gameplane/pull/1119)
