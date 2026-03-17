package main

// InternalHelloPlugin is a statically linked Go plugin.
type InternalHelloPlugin struct {}

func (p *InternalHelloPlugin) Init(api HostAPI) error {
	api.Log("Hello from Internal Plugin! F4 version is: " + api.GetVersion())
	return nil
}

func (p *InternalHelloPlugin) Close() error {
	return nil
}

func (p *InternalHelloPlugin) GetName() string {
	return "Internal Hello World"
}
