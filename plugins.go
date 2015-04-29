package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/dickeyxxx/gode"
)

// Plugin represents a javascript plugin
type Plugin struct {
	Topics   TopicSet   `json:"topics"`
	Commands CommandSet `json:"commands"`
}

var node = gode.NewClient(AppDir)

func init() {
	node.Registry = "http://174.129.255.150:4873"
	node.NodeVersion = "1.8.1"
	node.NpmVersion = "2.8.3"
}

// SetupNode sets up node and npm in ~/.heroku
func SetupNode() {
	if !node.IsSetup() {
		lock := getUpdateLock()
		defer lock.Unlock()
		Log("setting up plugins... ")
		if err := node.Setup(); err != nil {
			panic(err)
		}
		Logln("done")
	}
}

// LoadPlugins loads the topics and commands from the JavaScript plugins into the CLI
func (cli *Cli) LoadPlugins(plugins []Plugin) {
	for _, plugin := range plugins {
		for _, topic := range plugin.Topics {
			cli.AddTopic(topic)
		}
		for _, command := range plugin.Commands {
			if !cli.AddCommand(command) {
				Errf("WARNING: command %s has already been defined\n", command)
			}
		}
	}
}

var pluginsTopic = &Topic{
	Name:        "plugins",
	Description: "manage plugins",
}

var pluginsInstallCmd = &Command{
	Topic:       "plugins",
	Command:     "install",
	Args:        []Arg{{Name: "name"}},
	Description: "Installs a plugin into the CLI",
	Help: `Install a Heroku plugin

  Example:
  $ heroku plugins:install dickeyxxx/heroku-production-status`,

	Run: func(ctx *Context) {
		name := ctx.Args.(map[string]string)["name"]
		if len(name) == 0 {
			Errln("Must specify a plugin name")
			return
		}
		Errf("Installing plugin %s... ", name)
		if err := node.InstallPackage(name); err != nil {
			panic(err)
		}
		plugin := getPlugin(name)
		if plugin == nil || len(plugin.Commands) == 0 {
			Err("This does not appear to be a Heroku plugin, uninstalling... ")
			if err := (node.RemovePackage(name)); err != nil {
				panic(err)
			}
		}
		Errln("done")
	},
}

var pluginsUninstallCmd = &Command{
	Topic:       "plugins",
	Command:     "uninstall",
	Args:        []Arg{{Name: "name"}},
	Description: "Uninstalls a plugin from the CLI",
	Help: `Uninstalls a Heroku plugin

  Example:
  $ heroku plugins:uninstall heroku-production-status`,

	Run: func(ctx *Context) {
		name := ctx.Args.(map[string]string)["name"]
		Errf("Uninstalling plugin %s... ", name)
		if err := node.RemovePackage(name); err != nil {
			panic(err)
		}
		Errln("done")
	},
}

var pluginsListCmd = &Command{
	Topic:       "plugins",
	Description: "Lists the installed plugins",
	Help: `Lists installed plugins

  Example:
  $ heroku plugins`,

	Run: func(ctx *Context) {
		packages, err := node.Packages()
		if err != nil {
			panic(err)
		}
		for _, pkg := range packages {
			Println(pkg.Name, pkg.Version)
		}
	},
}

func runFn(module, topic, command string) func(ctx *Context) {
	return func(ctx *Context) {
		ctxJSON, err := json.Marshal(ctx)
		if err != nil {
			panic(err)
		}
		script := fmt.Sprintf(`
		var topic = '%s';
		var command = '%s';
		if (command === '') { command = null }
		require('%s')
		.commands.filter(function (c) {
			return c.topic === topic && c.command == command;
		})[0]
		.run(%s)`, topic, command, module, ctxJSON)

		// swallow sigint since the plugin will handle it
		swallowSignal(os.Interrupt)

		cmd := node.RunScript(script)
		cmd.Stdout = Stdout
		cmd.Stdin = os.Stdin
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			Exit(getExitCode(err))
		}
	}
}

func swallowSignal(s os.Signal) {
	c := make(chan os.Signal, 1)
	signal.Notify(c, s)
	go func() {
		<-c
	}()
}

func getExitCode(err error) int {
	exitErr, ok := err.(*exec.ExitError)
	if !ok {
		panic(err)
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	if !ok {
		panic(err)
	}
	return status.ExitStatus()
}

func getPlugin(name string) *Plugin {
	script := `console.log(JSON.stringify(require('` + name + `')))`
	cmd := node.RunScript(script)
	cmd.Stderr = Stderr
	output, err := cmd.StdoutPipe()
	if err != nil {
		panic(err)
	}
	if err := cmd.Start(); err != nil {
		panic(err)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(output)
	s := buf.String()
	var plugin Plugin
	err = json.Unmarshal(buf.Bytes(), &plugin)
	if err != nil {
		Errf("Error reading plugin: %s. See %s for more information.\n", name, ErrLogPath)
		Logln(err, "\n", s)
		return nil
	}
	if err := cmd.Wait(); err != nil {
		panic(err)
	}
	for _, command := range plugin.Commands {
		command.Plugin = name
		command.Run = runFn(name, command.Topic, command.Command)
	}
	return &plugin
}

// GetPlugins goes through all the node plugins and returns them in Go stucts
func GetPlugins() []Plugin {
	names := PluginNames()
	plugins := make([]Plugin, 0, len(names))
	for _, name := range names {
		plugin := getPlugin(name)
		if plugin != nil {
			plugins = append(plugins, *plugin)
		}
	}
	return plugins
}

// PluginNames just lists the files in ~/.heroku/node_modules
func PluginNames() []string {
	files, _ := ioutil.ReadDir(filepath.Join(AppDir, "node_modules"))
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name())
	}
	return names
}
