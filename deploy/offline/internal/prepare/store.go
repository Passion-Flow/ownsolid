package prepare

import (
	"context"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/configuration"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/engine"
	"github.com/Passion-Flow/ownsolid/deploy/offline/internal/scope"
)

type fixedStoreCommand struct {
	kind, target        string
	creation, reconcile bool
}

var storeCommands = map[string]fixedStoreCommand{
	"create-dataset-store-task":                 {kind: engine.DatasetStoreInitialize, target: "", creation: true, reconcile: false},
	"reconcile-dataset-store-task":              {kind: engine.DatasetStoreInitialize, target: "", creation: true, reconcile: true},
	"start-dataset-store-task":                  {kind: engine.DatasetStoreInitialize, target: "execute", creation: false, reconcile: false},
	"stop-dataset-store-task":                   {kind: engine.DatasetStoreInitialize, target: "stopped", creation: false, reconcile: false},
	"remove-dataset-store-task":                 {kind: engine.DatasetStoreInitialize, target: "removed", creation: false, reconcile: false},
	"reconcile-dataset-store-action":            {kind: engine.DatasetStoreInitialize, reconcile: true},
	"create-dataset-store-check-task":           {kind: engine.DatasetStoreCheck, target: "", creation: true, reconcile: false},
	"reconcile-dataset-store-check-task":        {kind: engine.DatasetStoreCheck, target: "", creation: true, reconcile: true},
	"start-dataset-store-check-task":            {kind: engine.DatasetStoreCheck, target: "execute", creation: false, reconcile: false},
	"stop-dataset-store-check-task":             {kind: engine.DatasetStoreCheck, target: "stopped", creation: false, reconcile: false},
	"remove-dataset-store-check-task":           {kind: engine.DatasetStoreCheck, target: "removed", creation: false, reconcile: false},
	"reconcile-dataset-store-check-action":      {kind: engine.DatasetStoreCheck, reconcile: true},
	"create-dataset-store-record-task":          {kind: engine.DatasetStoreRecord, target: "", creation: true, reconcile: false},
	"reconcile-dataset-store-record-task":       {kind: engine.DatasetStoreRecord, target: "", creation: true, reconcile: true},
	"start-dataset-store-record-task":           {kind: engine.DatasetStoreRecord, target: "execute", creation: false, reconcile: false},
	"stop-dataset-store-record-task":            {kind: engine.DatasetStoreRecord, target: "stopped", creation: false, reconcile: false},
	"remove-dataset-store-record-task":          {kind: engine.DatasetStoreRecord, target: "removed", creation: false, reconcile: false},
	"reconcile-dataset-store-record-action":     {kind: engine.DatasetStoreRecord, reconcile: true},
	"create-training-store-task":                {kind: engine.TrainingStoreInitialize, target: "", creation: true, reconcile: false},
	"reconcile-training-store-task":             {kind: engine.TrainingStoreInitialize, target: "", creation: true, reconcile: true},
	"start-training-store-task":                 {kind: engine.TrainingStoreInitialize, target: "execute", creation: false, reconcile: false},
	"stop-training-store-task":                  {kind: engine.TrainingStoreInitialize, target: "stopped", creation: false, reconcile: false},
	"remove-training-store-task":                {kind: engine.TrainingStoreInitialize, target: "removed", creation: false, reconcile: false},
	"reconcile-training-store-action":           {kind: engine.TrainingStoreInitialize, reconcile: true},
	"create-training-store-check-task":          {kind: engine.TrainingStoreCheck, target: "", creation: true, reconcile: false},
	"reconcile-training-store-check-task":       {kind: engine.TrainingStoreCheck, target: "", creation: true, reconcile: true},
	"start-training-store-check-task":           {kind: engine.TrainingStoreCheck, target: "execute", creation: false, reconcile: false},
	"stop-training-store-check-task":            {kind: engine.TrainingStoreCheck, target: "stopped", creation: false, reconcile: false},
	"remove-training-store-check-task":          {kind: engine.TrainingStoreCheck, target: "removed", creation: false, reconcile: false},
	"reconcile-training-store-check-action":     {kind: engine.TrainingStoreCheck, reconcile: true},
	"create-training-store-record-task":         {kind: engine.TrainingStoreRecord, target: "", creation: true, reconcile: false},
	"reconcile-training-store-record-task":      {kind: engine.TrainingStoreRecord, target: "", creation: true, reconcile: true},
	"start-training-store-record-task":          {kind: engine.TrainingStoreRecord, target: "execute", creation: false, reconcile: false},
	"stop-training-store-record-task":           {kind: engine.TrainingStoreRecord, target: "stopped", creation: false, reconcile: false},
	"remove-training-store-record-task":         {kind: engine.TrainingStoreRecord, target: "removed", creation: false, reconcile: false},
	"reconcile-training-store-record-action":    {kind: engine.TrainingStoreRecord, reconcile: true},
	"create-preparation-store-task":             {kind: engine.PreparationStoreInitialize, target: "", creation: true, reconcile: false},
	"reconcile-preparation-store-task":          {kind: engine.PreparationStoreInitialize, target: "", creation: true, reconcile: true},
	"start-preparation-store-task":              {kind: engine.PreparationStoreInitialize, target: "execute", creation: false, reconcile: false},
	"stop-preparation-store-task":               {kind: engine.PreparationStoreInitialize, target: "stopped", creation: false, reconcile: false},
	"remove-preparation-store-task":             {kind: engine.PreparationStoreInitialize, target: "removed", creation: false, reconcile: false},
	"reconcile-preparation-store-action":        {kind: engine.PreparationStoreInitialize, reconcile: true},
	"create-preparation-store-check-task":       {kind: engine.PreparationStoreCheck, target: "", creation: true, reconcile: false},
	"reconcile-preparation-store-check-task":    {kind: engine.PreparationStoreCheck, target: "", creation: true, reconcile: true},
	"start-preparation-store-check-task":        {kind: engine.PreparationStoreCheck, target: "execute", creation: false, reconcile: false},
	"stop-preparation-store-check-task":         {kind: engine.PreparationStoreCheck, target: "stopped", creation: false, reconcile: false},
	"remove-preparation-store-check-task":       {kind: engine.PreparationStoreCheck, target: "removed", creation: false, reconcile: false},
	"reconcile-preparation-store-check-action":  {kind: engine.PreparationStoreCheck, reconcile: true},
	"create-preparation-store-record-task":      {kind: engine.PreparationStoreRecord, target: "", creation: true, reconcile: false},
	"reconcile-preparation-store-record-task":   {kind: engine.PreparationStoreRecord, target: "", creation: true, reconcile: true},
	"start-preparation-store-record-task":       {kind: engine.PreparationStoreRecord, target: "execute", creation: false, reconcile: false},
	"stop-preparation-store-record-task":        {kind: engine.PreparationStoreRecord, target: "stopped", creation: false, reconcile: false},
	"remove-preparation-store-record-task":      {kind: engine.PreparationStoreRecord, target: "removed", creation: false, reconcile: false},
	"reconcile-preparation-store-record-action": {kind: engine.PreparationStoreRecord, reconcile: true},
}

func runStoreCommand(ctx context.Context, c configuration.Config, command fixedStoreCommand) (any, error) {
	if command.creation {
		return scope.CreateStoreTask(ctx, c, command.kind, command.reconcile)
	}
	return scope.StoreTaskAction(ctx, c, command.kind, command.target, command.reconcile)
}
