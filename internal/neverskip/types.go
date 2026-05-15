package neverskip

import "encoding/json"

type LoungeResp struct {
	S bool       `json:"S"`
	D LoungeData `json:"D"`
	F string     `json:"F"`
}

type LoungeData struct {
	AllowDownloadNBCGVideos string         `json:"allow_download_nb_cg_videos"`
	ItemList                []LoungeItem   `json:"item_list"`
	PrMonthYr               string         `json:"prmontyr"`
	PrevMonthYr             string         `json:"pervious_prmontr"`
}

type LoungeItem struct {
	Title   string             `json:"title"`
	Items   []LoungeAttachment `json:"item_list"`
	MoreCnt string             `json:"cnt"`
}

type LoungeAttachment struct {
	Src          string `json:"src"`
	FileLoc      string `json:"file_loc"`
	ThumbURL     string `json:"thump_url"`
	DownloadURL  string `json:"download_url"`
	Type         string `json:"type"`
	MsgID        string `json:"msg_id"`
	DeliveryStat string `json:"delivery_stat"`
	PinStat      string `json:"pin_stat"`
	YM           string `json:"ym"`
	Ext          string `json:"ext"`
}

type DailyNoticeResp struct {
	S bool            `json:"S"`
	D DailyNoticeData `json:"D"`
	F string          `json:"F"`
}

type DailyNoticeData struct {
	ItemList    []DailyNoticeItem `json:"item_list"`
	PrMonthYr   string            `json:"prmontyr"`
	TCnt        json.Number       `json:"tcnt"`
	PrevMonthYr string            `json:"pervious_prmontr"`
}

type DailyNoticeItem struct {
	Date        string         `json:"date"`
	Cont        string         `json:"cont"`
	Image       string         `json:"image"`
	Title       string         `json:"title"`
	MsgSrc      string         `json:"msg_src"`
	MsgType     string         `json:"msg_type"`
	PostFromImg string         `json:"post_from_img"`
	TestTar     DailyNoticeTar `json:"test_tar"`
}

type DailyNoticeTar struct {
	MsID       string          `json:"msid"`
	MSrc       string          `json:"msrc"`
	MBID       string          `json:"mbid"`
	DyStat     string          `json:"dystat"`
	PinStat    string          `json:"pinst"`
	ContSrc    string          `json:"cont_src"`
	MRID       string          `json:"mrid"`
	MTSP       string          `json:"mtsp"`
	SKey       string          `json:"skey"`
	MCat       string          `json:"mcat"`
	CatCd      string          `json:"catcd"`
	CatImg     string          `json:"catimg"`
	CTyp       string          `json:"ctyp"`
	CExt       string          `json:"cext"`
	MTit       string          `json:"mtit"`
	TImg       string          `json:"timg"`
	MCom       string          `json:"mcom"`
	TarCatID   string          `json:"tar_cat_id"`
	TarUser    string          `json:"tar_user"`
	MLan       string          `json:"mlan"`
	PImg       string          `json:"pimg"`
	MData      []json.RawMessage `json:"mdata"`
	PCnt       string          `json:"pcnt"`
}
