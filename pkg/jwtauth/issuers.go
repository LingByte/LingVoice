// Copyright (c) 2026 LingByte. All rights reserved.
// SPDX-License-Identifier: AGPL-3.0

package jwtauth

// Issuers separate access vs refresh JWTs so tokens are not interchangeable.
const (
	AccessIssuer  = "lingvoice"
	RefreshIssuer = "lingvoice.refresh"
)
