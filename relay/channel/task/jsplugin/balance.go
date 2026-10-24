package jsplugin

import (
	"context"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/QuantumNous/new-api/common"
	pluginruntime "github.com/QuantumNous/new-api/pkg/jsplugin"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/shopspring/decimal"
)

// QueryBalance uses the plugin's optional account hooks and returns USD, the
// existing channel balance storage unit. It never creates a generation task.
func (a *TaskAdaptor) QueryBalance(ctx context.Context) (float64, error) {
	if !a.plugin.Engine.HasExport("buildBalanceRequest") || !a.plugin.Engine.HasExport("parseBalanceResponse") {
		return 0, common.NewMessage("Task Plugin channels do not support balance queries")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	baseURL, key, proxy := a.info.ChannelBaseUrl, a.info.ApiKey, a.info.ChannelSetting.Proxy
	input := map[string]any{"baseUrl": baseURL}
	if err := a.applyUpstreamCredentials(input, a.info.ChannelType, key, proxy); err != nil {
		return 0, err
	}
	descriptor, err := a.requestDescriptor(ctx, "buildBalanceRequest", input)
	if err != nil {
		return 0, err
	}
	if descriptor.ResponseType != "" && descriptor.ResponseType != "json" || descriptor.BodyType != "" && descriptor.BodyType != "json" {
		return 0, fmt.Errorf("plugin balance requests support only JSON responses and bodies")
	}
	if err = pluginruntime.ValidateRequestHeaders(descriptor.Headers); err != nil {
		return 0, err
	}
	response, err := a.doFetchDescriptorContext(ctx, baseURL, key, proxy, descriptor)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return 0, fmt.Errorf("balance query returned HTTP %d", response.StatusCode)
	}
	const maxBalanceBytes = 256 << 10
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBalanceBytes+1))
	if err != nil {
		return 0, err
	}
	if len(body) > maxBalanceBytes {
		return 0, fmt.Errorf("balance response exceeds %d bytes", maxBalanceBytes)
	}
	var decoded any
	if err = common.Unmarshal(body, &decoded); err != nil {
		return 0, fmt.Errorf("invalid balance JSON response")
	}
	var result struct {
		Balance  *float64 `json:"balance"`
		Currency string   `json:"currency"`
	}
	if err = a.plugin.Engine.CallInto(ctx, &result, "parseBalanceResponse", input, map[string]any{
		"statusCode": response.StatusCode, "headers": response.Header, "body": decoded,
	}); err != nil {
		return 0, err
	}
	if result.Balance == nil || math.IsNaN(*result.Balance) || math.IsInf(*result.Balance, 0) {
		return 0, fmt.Errorf("plugin balance must be a finite number")
	}
	balance := *result.Balance
	switch result.Currency {
	case "USD":
	case "CNY":
		rate := operation_setting.USDExchangeRate
		if rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
			return 0, fmt.Errorf("USD exchange rate must be positive and finite")
		}
		balance = decimal.NewFromFloat(balance).Div(decimal.NewFromFloat(rate)).InexactFloat64()
	default:
		return 0, fmt.Errorf("plugin balance currency must be USD or CNY")
	}
	if math.IsNaN(balance) || math.IsInf(balance, 0) {
		return 0, fmt.Errorf("converted balance must be finite")
	}
	return balance, nil
}
