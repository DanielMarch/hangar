package domain

import "time"

// MarketOrder mirrors app.market_order / app.market_order_history.
// VolumeTotal, VolumeRemain and MinVolume are explicitly not money (§3.1);
// Escrow and Price are.
type MarketOrder struct {
	Owner          Owner
	OrderID        int64
	TypeID         int32
	RegionID       int32
	LocationID     int64
	Range          string // open vocabulary
	IsBuyOrder     bool
	IsCorporation  bool
	Escrow         *Money
	Price          Money
	VolumeTotal    int64
	VolumeRemain   int64
	MinVolume      *int64
	Duration       int32
	Issued         time.Time
	WalletDivision *int16
}

// MarketHistory mirrors app.market_history — global per region/type daily
// price history, not owner-scoped. OrderCount and Volume are not money.
type MarketHistory struct {
	RegionID   int32
	TypeID     int32
	Date       time.Time
	Average    *Money
	Highest    *Money
	Lowest     *Money
	OrderCount int64
	Volume     int64
}

// MarketPrice mirrors app.market_price — global adjusted/average prices.
type MarketPrice struct {
	TypeID        int32
	AdjustedPrice *Money
	AveragePrice  *Money
}
