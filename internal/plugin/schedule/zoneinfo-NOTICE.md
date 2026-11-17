# Frozen timezone data

`zoneinfo.zip` is copied unchanged from the pinned Go 1.26.10 toolchain's
`lib/time/zoneinfo.zip`. SHA-256: `8f55634d05f8bca1f7bc7c69c5933428c69357e0bdf565e5ba224e3f88ff12e8`.

The Go distribution states that this archive contains timezone files compiled
from the IANA Time Zone Database, which IANA asserts is in the public domain.
See https://www.iana.org/time-zones and Go's `lib/time/README`.

The archive is embedded in the owning schedule binary. Its package digest freezes
both calendar code and timezone rules; changing the archive requires a new plugin
version and the existing verification/activation gates. The method never reads
host timezone files or ZONEINFO. Historical NULL-method plans keep their original
native interpreter and its historical system-timezone dependency.
