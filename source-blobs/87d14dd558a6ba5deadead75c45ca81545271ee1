"""Stage the shared HTTP registration in a scratch Go build overlay."""

import argparse
import json
from pathlib import Path


def replace_once(source, anchor, replacement):
    if source.count(anchor) != 1:
        raise SystemExit(f"Expected one identity dispatcher anchor: {anchor!r}")
    return source.replace(anchor, replacement, 1)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("scratch", type=Path, help="existing directory outside the checkout")
    args = parser.parse_args()
    modules = Path(__file__).resolve().parents[3]
    handler = modules / "identity/http/handler.go"
    scratch = args.scratch.resolve(strict=True)
    if scratch.is_relative_to(modules.parents[3]):
        raise SystemExit("The overlay must be generated outside the checkout")

    source = handler.read_text(encoding="utf-8")
    imports = 'applicationhttp "github.com/Passion-Flow/ownsolid/services/control/internal/modules/applications/http"'
    productagent = "github.com/Passion-Flow/ownsolid/services/control/internal/modules/productagent"
    source = replace_once(source, imports, f'{imports}\n\t"{productagent}"\n\tproductagenthttp "{productagent}/http"')
    field = "Applications               *applicationhttp.Handler"
    source = replace_once(source, field, field + "\n\tProductAssistant *productagenthttp.Handler")
    app_service = "&applications.Service{DB: s.DB, Identity: s, Workspaces: w, Workflows: workflowService}"
    source = replace_once(source, f"Applications: &applicationhttp.Handler{{Service: {app_service}}}",
                          "Applications: &applicationhttp.Handler{Service: appService}, "
                          "ProductAssistant: &productagenthttp.Handler{Service: assistant}")
    constructor = "return &Handler{TelemetryLogs:"
    source = replace_once(source, constructor, f"appService := {app_service}\n\t"
                          f"assistant, assistantError := productagent.New(appService, nil)\n\t{constructor}")
    startup_errors = "errors.Join(licenseError, ssoError, localeError, p.err)"
    source = replace_once(source, startup_errors, startup_errors[:-1] + ", assistantError)")
    dispatch = "if applicationhttp.Owns(path) {"
    source = replace_once(source, dispatch, """if productagenthttp.Owns(path) {
		a, e := s.Authenticate(ctx, session, audience)
		if e != nil {
			return nil, 0, e
		}
		return h.ProductAssistant.Handle(r, a, requestID)
	}
	""" + dispatch)

    replacement = scratch / "identity-handler.go"
    replacement.write_text(source, encoding="utf-8")
    (scratch / "overlay.json").write_text(
        json.dumps({"Replace": {str(handler): str(replacement)}}) + "\n", encoding="utf-8"
    )


if __name__ == "__main__":
    main()
