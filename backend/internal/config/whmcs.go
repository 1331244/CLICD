package config

// BillingBlocked is independent of expiry, traffic and other operator restrictions.
// A billing installation may only remove its own named reason.
func (c Container) BillingBlocked() bool {
	for _, blocked := range c.BillingRestrictions {
		if blocked {
			return true
		}
	}
	return false
}
