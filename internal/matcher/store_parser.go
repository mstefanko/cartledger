package matcher

import (
	"regexp"
	"strings"
)

type Chain string

const (
	ChainOther    Chain = "other"
	ChainCostco   Chain = "costco"
	ChainShopRite Chain = "shoprite"
	ChainKroger   Chain = "kroger"
	ChainWalmart  Chain = "walmart"
	ChainTarget   Chain = "target"
)

type ParsedReceiptLine struct {
	StoreItemCode      string
	ReceiptDescription string
}

var costcoLeadingCodePattern = regexp.MustCompile(`^\s*([0-9]{1,7})\s+([^\r\n]+?)\s*$`)

func ClassifyStore(name string) Chain {
	normalized := strings.ToLower(strings.TrimSpace(name))
	normalized = strings.ReplaceAll(normalized, "-", " ")
	normalized = strings.Join(strings.Fields(normalized), " ")
	switch {
	case strings.Contains(normalized, "costco"):
		return ChainCostco
	case strings.Contains(normalized, "shoprite") || strings.Contains(normalized, "shop rite"):
		return ChainShopRite
	case strings.Contains(normalized, "kroger"):
		return ChainKroger
	case strings.Contains(normalized, "walmart") || strings.Contains(normalized, "wal mart"):
		return ChainWalmart
	case strings.Contains(normalized, "target"):
		return ChainTarget
	default:
		return ChainOther
	}
}

func ParseLine(rawName string, chain Chain) ParsedReceiptLine {
	switch chain {
	case ChainCostco:
		return parseCostcoLine(rawName)
	default:
		return ParsedReceiptLine{}
	}
}

func parseCostcoLine(rawName string) ParsedReceiptLine {
	if strings.ContainsAny(rawName, "\r\n") {
		return ParsedReceiptLine{}
	}
	match := costcoLeadingCodePattern.FindStringSubmatch(rawName)
	if len(match) != 3 {
		return ParsedReceiptLine{}
	}
	description := strings.TrimSpace(match[2])
	if description == "" {
		return ParsedReceiptLine{}
	}
	return ParsedReceiptLine{
		StoreItemCode:      match[1],
		ReceiptDescription: description,
	}
}
