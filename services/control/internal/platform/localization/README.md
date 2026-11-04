# Offline locale and asset registry

This package admits immutable local language packages and serves their public bytes to the frontend and mail renderer. It performs no translation API call and carries no provider credential. Language manufacturing, runtime installation and human translation review are different responsibilities.

## Source, manufacturing and runtime boundaries

[registry.go](registry.go) embeds the current English/Simplified-Chinese seed resources and pinned font lock. `New` validates a typed `LocalizationConfiguration` and `LocalizationPolicy`, loads the optional offline package directory, then verifies every required locale is actually available. `Default` is a process singleton for the seed registry; production composition explicitly constructs and injects the selected registry.

The central English UI source lives in [frontend i18n](../../../../../web/README.md18n/README.md), with its mail/plural source inputs. Development candidates live under [resources/localization/candidates](../../../../../resources/localization/candidates); they are not served from that directory and are not automatically runtime packages. [Static packaging](../../../../../tools/localization/static_product_package.py) produces the separate hash-addressed runtime inventory and a private manufacturing-provenance sidecar in an explicitly separate sibling directory. That sidecar must not enter the public asset whitelist.

The [static-product contract](../../../../../docs/milestones/m16-localization/static-product-resources.md) describes source binding, editorial/delta provenance and packaging. Preserve the exact source digest across extraction, manufacture, seed generation and inventory admission. Candidate counts or README editions do not establish installed runtime coverage.

## Admission and immutable serving

`load` reads `inventory.json` from a private directory through bounded file access. It checks exact names/media types, file sizes/hashes, aggregate budgets, source/catalog binding and a closed directory inventory; extra files and nested content fail admission. Locale objects pass strict schema, key/placeholder/template/metadata and alias checks. Original font and license identities must match [font_assets.json](font_assets.json); actual declared weight/style remains part of the lock.

[static_product.go](static_product.go) admits the distinct machine-generated structure-checked path with human review pending, runtime translation disabled and upstream model identity explicitly unverified. It does not grant Google-reviewed status. Exact-product-string cmap coverage is not general script shaping or native-language review. Unsupported `Intl` capabilities retain explicit metadata/fallback rather than fabricated locale support.

[ServeHTTP](http.go) exposes GET-only `/api/v1/localization/catalog` and digest-addressed package/font/asset URLs. Queries, alternate raw paths, unknown digests and incorrect categories fail. Catalog responses are not cached; admitted content uses a digest ETag and configured immutable-cache lifetime. The registry does not browse the filesystem in response to a caller-supplied path.

## Consumers, configuration and recovery

[Server composition](../../../cmd/server/main.go) injects the registry into identity/profile and [mail](../mail/README.md). `Catalog` returns detached metadata; `MailTemplates`/`MailPresentation` expose admitted resources. Missing stored mail locales recover through English, while a configured required locale missing at startup fails admission.

[config/localization.go](../config/localization.go) owns `localization.package_directory`, `required_locales` and runtime package/font/aggregate/message/locale/font-count/cache bounds. Use [deployment env inputs](../../../../../config/examples/localization/deployment.env.example) and [the configuration guide](../../../../../docs/milestones/m16-localization/configuration.md). There is no in-place hot-reload API: publish a completely verified package, retain the previous package and restart through the owning deployment lifecycle. Never copy candidate JSON or private provenance into a live inventory directory as a repair.

## Verification

With the pinned toolchain and admitted cache/temp roots:

```sh
go -C services/control test ./internal/platform/localization
```

These tests exercise structural admission, HTTP bytes, hashes/ETags, seed integrity and machine/review distinctions. [assets_integration_test.go](assets_integration_test.go) separately requires `localization_assets_integration` and an actual owned original-font package. The [browser catalog driver](../../../../../tools/quality/localization/static-catalog-browser.md) checks installed frontend resources; neither layer substitutes for semantic or native-language review. When a source message changes, synchronize manufacture, seeds, package contracts, frontend formatting and mail templates rather than relaxing source admission.

[mail_validation_test.go](mail_validation_test.go) mirrors the static manufacturer's
template-action, unmatched-delimiter and HTML-literal controls at the runtime
boundary. Its synthetic RTL/entity positives do not establish translation quality
or SMTP delivery. This static-product group is included by the package test
command above; the historical `runtime.py` wrapper selects the separate
`TestLocalization` group and does not execute these static-product cases.

`ServeHTTPWithPolicy` shares the control HTTP owner's configured retry advice for
errors. The standard `ServeHTTP` compatibility entry uses `DefaultHTTPPolicy`;
asset admission, caching and authorization do not acquire another policy loader.
The main dispatcher injects its effective policy explicitly.


## Current cumulative installation

The current localization profile inherits Portal and uses immutable source-bound
registry resources without a runtime provider. The static en/ar/fr component and
separate en/zh original-font component pass actual HTTP/SMTP/browser checks,
including RTL and real loaded fonts. Native same-binary restart/required-missing
refusal/rollback preserves unavailable saved locales. These subset proofs do not
close the exact202 resource gate or native human review. The
[current operation guide](../../../../../docs/milestones/m16-localization/cumulative-operation.md)
and [quality owner](../../../../../tools/quality/localization/README.md) retain
commands, configuration, failure recovery and actual evidence boundaries.


Current profile-response repair uses selection/session/form generations and
17 settled static/browser stages for both entrances. Font sampling waits for
main readiness without lowering face-count or integrity assertions. The unchanged
seed/font-resource proof stays at its recorded baseline scope. Native human and
full202 resource review remain pending; see the current M16 repair handoff.
