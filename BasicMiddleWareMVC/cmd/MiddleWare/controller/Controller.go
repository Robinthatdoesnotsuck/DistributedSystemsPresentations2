package controller

type ServiceTypes string

const (
	Undefined    ServiceTypes = ""
	Trojan                    = "trojan"
	ReverseShell              = "reverseShell"
	AdWare                    = "adWare"
	RansomWare                = "ransomWare"
	BombWare                  = "bombWare"
)

type ServiceList struct{}

type MWareController struct{}
