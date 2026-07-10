package handlers

import "github.com/U188/octopus/internal/model"

func redactSiteResponse(site *model.Site) {
	if site == nil {
		return
	}
	for i := range site.Accounts {
		site.Accounts[i].RedactSecrets()
	}
}

func redactSiteListResponse(sites []model.Site) {
	for i := range sites {
		redactSiteResponse(&sites[i])
	}
}

func redactSiteAccountResponse(account *model.SiteAccount) {
	if account != nil {
		account.RedactSecrets()
	}
}
