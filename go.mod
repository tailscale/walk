module github.com/tailscale/walk

go 1.17

require (
	github.com/lxn/walk v0.0.0-20210112085537-c389da54e794
	github.com/lxn/win v0.0.0-20210218163916-a377121e959e
	golang.org/x/sys v0.0.0-20211102061401-a2f17f7b995c
	gopkg.in/Knetic/govaluate.v3 v3.0.0
)

replace (
	github.com/lxn/walk => github.com/tailscale/walk v0.0.0-20220506150313-ed127cfb919a
	github.com/lxn/win => github.com/tailscale/win v0.0.0-20221118185520-e8ccca099752
)
