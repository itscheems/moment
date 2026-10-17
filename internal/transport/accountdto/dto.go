package accountdto

import "github.com/moment/moment/internal/domain"

// Public carries the identity a reader can see, without administrative state.
type Public struct {
	UserID        string  `json:"user_id"`
	Username      string  `json:"username"`
	DisplayName   string  `json:"display_name"`
	Bio           string  `json:"bio"`
	AvatarMediaID *string `json:"avatar_media_id"`
	CoverMediaID  *string `json:"cover_media_id"`
	ProfileURL    string  `json:"profile_url"`
}

// Private adds account state for the account itself and administration screens.
type Private struct {
	Public
	Role           string `json:"role"`
	Status         string `json:"status"`
	UsernameChosen bool   `json:"username_chosen"`
}

func PublicFromUser(user domain.User) Public {
	return Public{UserID: user.ID, Username: user.Username, DisplayName: user.DisplayName, Bio: user.Bio, AvatarMediaID: user.AvatarMediaID, CoverMediaID: user.CoverMediaID, ProfileURL: domain.ProfileURL(user.Username)}
}

func PrivateFromUser(user domain.User) Private {
	return Private{Public: PublicFromUser(user), Role: string(user.Role), Status: string(user.Status), UsernameChosen: user.UsernameSetAtMS != nil}
}
