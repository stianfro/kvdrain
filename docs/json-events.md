# JSON events

`--json` writes newline-delimited JSON. Each line is a complete event. Consumers must ignore unknown fields and unknown event types.

```json
{"apiVersion":"kvdrain.io/v1alpha1","kind":"Event","time":"2026-07-13T10:00:00Z","runID":"4f0c8a21","type":"migration","node":"worker-3","object":{"kind":"VirtualMachineInstanceMigration","namespace":"default","name":"kubevirt-evacuation-abc"},"state":"running","details":{"source":"worker-3","target":"worker-4","vmi":"vm-a","retry":0}}
```

Required envelope fields are `apiVersion`, `kind`, `time`, `runID`, `type`, and `state`. `node`, `object`, `message`, and `details` depend on the event.

Common types are `run`, `node`, `pod`, `vmi`, `migration`, `xfer`, `hotplug`, `diagnostic`, and `summary`. Events are emitted on state or detail changes. They are not a fixed-rate metrics stream.

The current schema version is `kvdrain.io/v1alpha1`. An incompatible envelope change will use a new version.
