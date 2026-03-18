package main

// InternalHelloPlugin is a statically linked Go plugin.
type InternalHelloPlugin struct {}

func (p *InternalHelloPlugin) Init(api HostAPI) error {
	api.Message("Hello from Internal Go Plugin! F4 version: " + api.GetVersion())
	return nil
}

func (p *InternalHelloPlugin) Close() error {
	return nil
}

func (p *InternalHelloPlugin) GetName() string {
	return "Internal Hello World"
}
