package main

import (
	"encoding/json"
	"fmt"
	"github.com/tidwall/gjson"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
)

/**** 每日一句模板
{{content.DATA}}
{{note.DATA}}
{{translation.DATA}}
*/
/****
golang 发送消息模板API-demo
*/

var (
	APPID          = "wxe30f6e92df7b9cd6"
	APPSECRET      = "e7ceb6a9642e1f4b61348cb79cafaa4c"
	SentTemplateID = "-b1MHuYqjKRZSCdXhMBJa9Dm7OkqqSUdgU0ZuTNHvQA" //每日一句的模板ID，替换成自己的
)

//IPListRes 获取微信服务器IP地址 返回结果
type IPListRes struct {
	IPList []string `json:"ip_list"`
}
type token struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

type sentence struct {
	Content     string `json:"content"`
	Note        string `json:"note"`
	Translation string `json:"translation"`
}

func main() {
	//spec := "0 0 12 * * *" // 每天12:00
	//spec1 := "0 0 7 * * *" // 每天早晨7:00
	//c := cron.New()
	//c.AddFunc(spec, everydaysen)
	//c.AddFunc(spec1, weather)
	//c.Start()
	//fmt.Println("开启定时任务")
	//select {}

	//everydaysen()
	getWechatServerIP()

}

//获取微信accesstoken
func getaccesstoken() string {
	url := fmt.Sprintf("https://api.weixin.qq.com/cgi-bin/token?grant_type=client_credential&appid=%v&secret=%v", APPID, APPSECRET)
	resp, err := http.Get(url)
	if err != nil {
		fmt.Println("获取微信token失败", err)
		return ""
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("微信token读取失败", err)
		return ""
	}

	token := token{}
	err = json.Unmarshal(body, &token)
	if err != nil {
		fmt.Println("微信token解析json失败", err)
		return ""
	}
	log.Print(token.AccessToken)
	return token.AccessToken
}

//发送每日一句
func everydaysen() {
	req, fxurl := getsen()
	if req.Content == "" {
		return
	}
	access_token := getaccesstoken()
	if access_token == "" {
		return
	}

	flist := getflist(access_token)
	if flist == nil {
		return
	}

	reqdata := "{\"content\":{\"value\":\"" + req.Content + "\", \"color\":\"#0000CD\"}, \"note\":{\"value\":\"" + req.Note + "\"}, \"translation\":{\"value\":\"" + req.Translation + "\"}}"
	for _, v := range flist {
		templatepost(access_token, reqdata, fxurl, SentTemplateID, v.Str)
	}
}

//获取每日一句
func getsen() (sentence, string) {
	resp, err := http.Get("http://open.iciba.com/dsapi/?date")
	sent := sentence{}
	if err != nil {
		fmt.Println("获取每日一句失败", err)
		return sent, ""
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("读取内容失败", err)
		return sent, ""
	}

	err = json.Unmarshal(body, &sent)
	if err != nil {
		fmt.Println("每日一句解析json失败")
		return sent, ""
	}
	fenxiangurl := gjson.Get(string(body), "fenxiang_img").String()
	fmt.Println(sent)
	return sent, fenxiangurl
}

//获取关注者列表
func getflist(access_token string) []gjson.Result {
	url := "https://api.weixin.qq.com/cgi-bin/user/get?access_token=" + access_token + "&next_openid="
	resp, err := http.Get(url)
	if err != nil {
		fmt.Println("获取关注列表失败", err)
		return nil
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println("读取内容失败", err)
		return nil
	}
	flist := gjson.Get(string(body), "data.openid").Array()
	log.Print(flist)
	return flist
}

//发送模板消息
func templatepost(access_token string, reqdata string, fxurl string, templateid string, openid string) {
	url := "https://api.weixin.qq.com/cgi-bin/message/template/send?access_token=" + access_token

	reqbody := "{\"touser\":\"" + openid + "\", \"template_id\":\"" + templateid + "\", \"url\":\"" + fxurl + "\", \"data\": " + reqdata + "}"

	resp, err := http.Post(url,
		"application/x-www-form-urlencoded",
		strings.NewReader(string(reqbody)))
	if err != nil {
		fmt.Println(err)
		return
	}

	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(string(body))
}

//获取微信服务器ip
func getWechatServerIP() {
	access_token := getaccesstoken()
	if access_token == "" {
		return
	}
	url := "https://api.weixin.qq.com/cgi-bin/get_api_domain_ip?access_token=" + access_token
	resp, err := http.Get(url)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println(string(body))
	ipListRes := &IPListRes{}
	err = json.Unmarshal(body, &ipListRes)
	if err != nil {
		fmt.Println(err)
	}
	//a := ipListRes.IPList[0:1]
	//fmt.Printf("%s",a)
	fmt.Println("微信服务器地址：")
	for index, value := range ipListRes.IPList {
		fmt.Printf("index: %d value: %s\n", index, value)
	}
}
