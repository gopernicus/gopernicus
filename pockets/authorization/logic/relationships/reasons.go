package relationships

import authmodel "github.com/gopernicus/gopernicus/pockets/authorization/logic/model"

func outcomeReason(allowed bool) authmodel.Reason {
	if allowed {
		return authmodel.ReasonGranted
	}
	return authmodel.ReasonDenied
}
