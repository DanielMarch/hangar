package domain

import "time"

// Contract mirrors app.contract. Type, Status and Availability are CCP open
// vocabularies (Principle 14), not Go enums. Price/Reward/Collateral/Buyout
// are money; Volume (m3) is explicitly not.
type Contract struct {
	Owner               Owner
	ContractID          int64
	IssuerID            int64
	IssuerCorporationID int64
	AssigneeID          *int64
	AcceptorID          *int64
	StartLocationID     *int64
	EndLocationID       *int64
	Type                string
	Status              string
	Title               *string
	ForCorporation      bool
	Availability        string
	DateIssued          time.Time
	DateExpired         time.Time
	DateAccepted        *time.Time
	DaysToComplete      *int32
	DateCompleted       *time.Time
	Price               *Money
	Reward              *Money
	Collateral          *Money
	Buyout              *Money
	Volume              *float64
}

// ContractItem mirrors app.contract_item. Quantity and Runs are not money.
type ContractItem struct {
	Owner              Owner
	ContractID         int64
	RecordID           int64
	TypeID             int32
	Quantity           int64
	RawQuantity        *int64
	IsSingleton        bool
	IsIncluded         bool
	IsBlueprintCopy    *bool
	MaterialEfficiency *int16
	TimeEfficiency     *int16
	Runs               *int32
}

// ContractBid mirrors app.contract_bid.
type ContractBid struct {
	Owner      Owner
	ContractID int64
	BidID      int64
	BidderID   int64
	DateBid    time.Time
	Amount     Money
}
