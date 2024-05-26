package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/brutella/hap"
	"github.com/brutella/hap/accessory"
	"github.com/spf13/cobra"
	"github.com/waynezhang/aiseg-homekit/internal/aisegmanager"
	"github.com/waynezhang/aiseg-homekit/internal/log"
)

var HKServeCmd = func() *cobra.Command {
	var interval int
	var dbPath string
	fn := func(cmd *cobra.Command, args []string) {
		serve(interval, dbPath)
	}

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start HomeKit server. AISEG_USER and AIUSEG_PASSWORD are required as environment variables. PIN code (00102003 by default) can be configured by AISEG_PIN.",
		Run:   fn,
	}
	cmd.Flags().IntVarP(&interval, "interval", "i", 1, "Refresh interval")
	cmd.Flags().StringVarP(&dbPath, "db", "d", "./db", "Database path")

	return cmd
}()

func serve(interval int, dbPath string) {
	mgr, bridge, accessories, setterMap := discoverAccessories()
	if len(accessories) == 0 {
		log.E("No accessories found")
		os.Exit(1)
	}
	log.D("Found %d accessories", len(accessories))

	store := hap.NewFsStore(dbPath)

	server, err := hap.NewServer(store, bridge.A, accessories...)
	if err != nil {
		log.F("Failed to create server due to %s", err.Error())
	}

	iface := os.Getenv("AISEG_IFACE")
	if iface != "" {
		log.D("Binding to iface %s", iface)
		server.Ifaces = []string{iface}
	}

	pin := os.Getenv("AISEG_PIN")
	if pin == "" {
		pin = "00102003"
	}
	server.Pin = pin

	log.D("Database path %s", dbPath)
	log.D("Refresh interval %dmin...", interval)
	startRefresh(mgr, setterMap, interval)

	log.D("Starting health check handler")
	startHealthCheckHandler(server)

	fmt.Printf("Starting server with PIN code %s...\n", server.Pin)
	if err = server.ListenAndServe(context.Background()); err != nil {
		log.F("Failed to start server due to %s", err.Error())
	}
}

func startHealthCheckHandler(server *hap.Server) {
	server.ServeMux().HandleFunc("/health", func(res http.ResponseWriter, req *http.Request) {
		res.Write([]byte("OK"))
	})
}

type valueSetter func(bool)

func discoverAccessories() (*aisegmanager.AiSEGManager, *accessory.Bridge, []*accessory.A, map[string]valueSetter) {
	log.D("Discovering devices")

	accessories := []*accessory.A{}
	setterMap := map[string]valueSetter{}
	mgr := aisegmanager.DiscoverNewAiSEGManager()
	for idx, d := range mgr.Devices {
		switch d.Type {
		case aisegmanager.DeviceTypeLight:
			a := accessory.NewLightbulb(accessory.Info{
				Name: d.Name,
			})
			a.Lightbulb.On.OnValueRemoteUpdate(func(on bool) {
				_ = mgr.TurnDevice(d.NodeId, on)
			})
			a.Id = uint64(10000 + idx)
			a.Lightbulb.On.SetValue(d.IsOn)

			accessories = append(accessories, a.A)
			setterMap[d.NodeId] = func(on bool) {
				a.Lightbulb.On.SetValue(on)
			}
		case aisegmanager.DeviceTypeFloorHeating:
			a := accessory.NewFan(accessory.Info{
				Name: d.Name,
			})
			a.Fan.On.OnSetRemoteValue(func(on bool) error {
				return mgr.TurnDevice(d.NodeId, on)
			})
			a.Id = uint64(10000 + idx)
			a.Fan.On.SetValue(d.IsOn)
			accessories = append(accessories, a.A)
			setterMap[d.NodeId] = func(on bool) {
				a.Fan.On.SetValue(on)
			}
		}
	}

	for _, a := range accessories {
		log.D("Created accessory %12d %s", a.Id, a.Name())
	}

	bridge := accessory.NewBridge(accessory.Info{
		Name:         mgr.Name,
		Model:        mgr.Model,
		Manufacturer: "Panasonic",
	})
	bridge.Id = 1

	return mgr, bridge, accessories, setterMap
}

func startRefresh(mgr *aisegmanager.AiSEGManager, setterMap map[string]valueSetter, interval int) {
	go func() {
		for {
			time.Sleep(time.Duration(interval) * time.Minute)
			log.D("Refreshing tokens")
			mgr.Refresh()

			for _, d := range mgr.Devices {
				setter := setterMap[d.NodeId]
				if setter != nil {
					setter(d.IsOn)
				}
			}
		}
	}()
}
