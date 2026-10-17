module github.com/gopernicus/gopernicus/pockets/secrets

go 1.26.1

require github.com/gopernicus/gopernicus/sdk v0.12.0

// Pre-tag: cryptids.KeyRing ships in the next sdk release. Dropped at S4.
replace github.com/gopernicus/gopernicus/sdk => ../../sdk
