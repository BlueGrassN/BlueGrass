// Compile only. This file calls no API and never opens or modifies WFP.
#include <windows.h>
#include <fwpmu.h>
#include <stddef.h>

static_assert(sizeof(FWPM_FILTER0) == 200, "FWPM_FILTER0");
static_assert(offsetof(FWPM_FILTER0, filterCondition) == 120, "conditions");
static_assert(offsetof(FWPM_FILTER0, rawContext) == 152, "context union");
static_assert(offsetof(FWPM_FILTER0, filterId) == 176, "filter id");
static_assert(offsetof(FWPM_FILTER0, effectiveWeight) == 184, "weight");
static_assert(sizeof(FWPM_FILTER_CONDITION0) == 40, "condition");
static_assert(offsetof(FWPM_FILTER_CONDITION0, conditionValue) == 24, "value");
static_assert(sizeof(FWPM_FILTER_ENUM_TEMPLATE0) == 72, "enum template");
static_assert(sizeof(FWPM_PROVIDER0) == 64, "provider");
static_assert(sizeof(FWPM_SUBLAYER0) == 72, "sublayer");
static_assert(sizeof(FWPM_SESSION0) == 72, "session");
static_assert(sizeof(FWPM_LAYER0) == 72, "layer");
static_assert(FWP_FILTER_ENUM_FLAG_INCLUDE_BOOTTIME == 8, "boot enumeration");
static_assert(FWP_FILTER_ENUM_FLAG_INCLUDE_DISABLED == 16, "disabled enumeration");
static_assert(FWPM_FILTER_FLAG_PERSISTENT == 1, "persistent flag");
static_assert(FWPM_FILTER_FLAG_BOOTTIME == 2, "boot flag");
static_assert(FWP_MATCH_NOT_EQUAL == 10, "DNS exclusion match");
static_assert(FWP_ACTION_BLOCK == 0x1001, "block action");
static_assert(FWP_ACTION_PERMIT == 0x1002, "permit action");
