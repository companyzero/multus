module multus

go 1.19

require (
	github.com/jrick/ss v0.9.1
	github.com/smtc/rsync v0.0.0-00010101000000-000000000000
	golang.org/x/sync v0.3.0
	golang.org/x/term v0.9.0
	gopkg.in/yaml.v2 v2.4.0
)

require (
	github.com/companyzero/sntrup4591761 v0.0.0-20200131011700-2b0d299dbd22 // indirect
	github.com/dchest/blake2b v1.0.0 // indirect
	github.com/smtc/rollsum v0.0.0-20150721100732-39e98d252100 // indirect
	github.com/smtc/seekbuffer v0.0.0-20151009054628-711359748967 // indirect
	golang.org/x/crypto v0.0.0-20210921155107-089bfa567519 // indirect
	golang.org/x/sys v0.9.0 // indirect
)

replace github.com/smtc/rsync => github.com/dajohi/rsync v0.0.0-20220210212722-7c40f7496082
