# Block device

## Arch

Unix socket -> cow [ -> mem cache -> file cache -> cow layer [ -> batcher (+signal) -> bucket ] ] -> (readonly) -> prefetch -> chunker -> mem cache -> file cache -> bucket
