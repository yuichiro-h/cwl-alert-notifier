package main

type Alarm struct {
	AlarmName        string       `json:"AlarmName"`
	AlarmDescription *string      `json:"AlarmDescription"`
	AWSAccountID     string       `json:"AWSAccountId"`
	NewStateValue    string       `json:"NewStateValue"`
	NewStateReason   string       `json:"NewStateReason"`
	StateChangeTime  string       `json:"StateChangeTime"`
	Region           string       `json:"Region"`
	OldStateValue    string       `json:"OldStateValue"`
	Trigger          AlarmTrigger `json:"Trigger"`
}

type AlarmTrigger struct {
	MetricName                       string   `json:"MetricName"`
	Namespace                        string   `json:"Namespace"`
	StatisticType                    string   `json:"StatisticType"`
	Statistic                        string   `json:"Statistic"`
	Unit                             *string  `json:"Unit"`
	Dimensions                       []string `json:"Dimensions"`
	Period                           int      `json:"Period"`
	EvaluationPeriods                int      `json:"EvaluationPeriods"`
	ComparisonOperator               string   `json:"ComparisonOperator"`
	Threshold                        float64  `json:"Threshold"`
	TreatMissingData                 string   `json:"TreatMissingData"`
	EvaluateLowSampleCountPercentile string   `json:"EvaluateLowSampleCountPercentile"`
}
