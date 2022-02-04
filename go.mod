module multus

go 1.17

require (
	github.com/jrick/ss v0.9.1
	github.com/smtc/rsync v0.0.0-20151014010438-0a038bb0deb8
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	golang.org/x/term v0.0.0-20201126162022-7de9c90e9dd1
	gopkg.in/yaml.v2 v2.4.0
)

require (
	github.com/companyzero/sntrup4591761 v0.0.0-20200131011700-2b0d299dbd22 // indirect
	github.com/dchest/blake2b v1.0.0 // indirect
	github.com/smtc/rollsum v0.0.0-20150721100732-39e98d252100 // indirect
	github.com/smtc/seekbuffer v0.0.0-20151009054628-711359748967 // indirect
	golang.org/x/crypto v0.0.0-20220112180741-5e0467b6c7ce // indirect
	golang.org/x/sys v0.0.0-20210615035016-665e8c7367d1 // indirect
)

replace github.com/smtc/rsync => github.com/dajohi/rsync v0.0.0-20220210212722-7c40f7496082
