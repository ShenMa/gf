// Copyright 2018 gf Author(https://gitee.com/johng/gf). All Rights Reserved.
//
// This Source Code Form is subject to the terms of the MIT License.
// If a copy of the MIT was not distributed with this file,
// You can obtain one at https://gitee.com/johng/gf.
// 路由控制基本方法.

package ghttp

import (
    "errors"
    "strings"
    "container/list"
    "gitee.com/johng/gf/g/util/gregex"
    "gitee.com/johng/gf/g/util/gstr"
)


// 解析pattern
func (s *Server)parsePattern(pattern string) (domain, method, uri string, err error) {
    uri    = pattern
    domain = gDEFAULT_DOMAIN
    method = gDEFAULT_METHOD
    if array, err := gregex.MatchString(`([a-zA-Z]+):(.+)`, pattern); len(array) > 1 && err == nil {
        method = array[1]
        uri    = array[2]
    }
    if array, err := gregex.MatchString(`(.+)@([\w\.\-]+)`, uri); len(array) > 1 && err == nil {
        uri     = array[1]
        domain  = array[2]
    }
    if uri == "" {
        err = errors.New("invalid pattern")
    }
    // 去掉末尾的"/"符号，与路由匹配时处理一直
    if uri != "/" {
        uri = strings.TrimRight(uri, "/")
    }
    return
}

// 路由注册处理方法。
// 如果带有hook参数，表示是回调注册方法，否则为普通路由执行方法。
func (s *Server) setHandler(pattern string, handler *handlerItem, hook ... string) error {
    // Web Server正字运行时无法动态注册路由方法
    if s.Status() == SERVER_STATUS_RUNNING {
        return errors.New("cannot bind handler while server running")
    }
    var hookName string
    if len(hook) > 0 {
        hookName = hook[0]
    }
    domain, method, uri, err := s.parsePattern(pattern)
    if err != nil {
        return errors.New("invalid pattern")
    }

    // 路由对象
    handler.router = &Router {
        Uri      : uri,
        Domain   : domain,
        Method   : method,
        Priority : strings.Count(uri[1:], "/"),
    }
    handler.router.RegRule, handler.router.RegNames = s.patternToRegRule(uri)

    // 动态注册，首先需要判断是否是动态注册，如果不是那么就没必要添加到动态注册记录变量中。
    // 非叶节点为哈希表检索节点，按照URI注册的层级进行高效检索，直至到叶子链表节点；
    // 叶子节点是链表，按照优先级进行排序，优先级高的排前面，按照遍历检索，按照哈希表层级检索后的叶子链表数据量不会很大，所以效率比较高；
    tree := (map[string]interface{})(nil)
    if len(hookName) == 0 {
        tree = s.serveTree
    } else {
        tree = s.hooksTree
    }
    if _, ok := tree[domain]; !ok {
        tree[domain] = make(map[string]interface{})
    }
    // 用于遍历的指针
    p := tree[domain]
    if len(hookName) > 0 {
        if _, ok := p.(map[string]interface{})[hookName]; !ok {
            p.(map[string]interface{})[hookName] = make(map[string]interface{})
        }
        p = p.(map[string]interface{})[hookName]
    }
    // 当前节点的规则链表
    lists := make([]*list.List, 0)
    array := ([]string)(nil)
    if strings.EqualFold("/", uri) {
        array = []string{"/"}
    } else {
        array = strings.Split(uri[1:], "/")
    }
    // 键名"*fuzz"代表模糊匹配节点，其下会有一个链表；
    // 键名"*list"代表链表，叶子节点和模糊匹配节点都有该属性；
    for k, v := range array {
        if len(v) == 0 {
            continue
        }
        // 判断是否模糊匹配规则
        if gregex.IsMatchString(`^[:\*]|\{[\w\.\-]+\}|\*`, v) {
            v = "*fuzz"
            // 由于是模糊规则，因此这里会有一个*list，用以将后续的路由规则加进来，
            // 检索会从叶子节点的链表往根节点按照优先级进行检索
            if v, ok := p.(map[string]interface{})["*list"]; !ok {
                p.(map[string]interface{})["*list"] = list.New()
                lists = append(lists, p.(map[string]interface{})["*list"].(*list.List))
            } else {
                lists = append(lists, v.(*list.List))
            }
        }
        // 属性层级数据写入
        if _, ok := p.(map[string]interface{})[v]; !ok {
            p.(map[string]interface{})[v] = make(map[string]interface{})
        }
        p = p.(map[string]interface{})[v]
        // 到达叶子节点，往list中增加匹配规则(条件 v != "*fuzz" 是因为模糊节点的话在前面已经添加了*list链表)
        if k == len(array) - 1 && v != "*fuzz" {
            if v, ok := p.(map[string]interface{})["*list"]; !ok {
                p.(map[string]interface{})["*list"] = list.New()
                lists = append(lists, p.(map[string]interface{})["*list"].(*list.List))
            } else {
                lists = append(lists, v.(*list.List))
            }
        }
    }
    // 得到的lists是该路由规则一路匹配下来相关的模糊匹配链表(注意不是这棵树所有的链表)，
    // 从头开始遍历每个节点的模糊匹配链表，将该路由项插入进去(按照优先级高的放在前面)
    item := (*handlerItem)(nil)
    for _, l := range lists {
        pushed  := false
        for e := l.Front(); e != nil; e = e.Next() {
            item = e.Value.(*handlerItem)
            // 判断是否已存在相同的路由注册项
            if len(hookName) == 0 {
                if strings.EqualFold(handler.router.Domain, item.router.Domain) &&
                    strings.EqualFold(handler.router.Method, item.router.Method) &&
                    strings.EqualFold(handler.router.Uri, item.router.Uri) {
                    e.Value = handler
                    pushed = true
                    break
                }
            }
            if s.compareRouterPriority(handler.router, item.router) {
                l.InsertBefore(handler, e)
                pushed = true
                break
            }
        }
        if !pushed {
            l.PushBack(handler)
        }
    }
    //gutil.Dump(s.serveTree)
    //gutil.Dump(s.hooksTree)
    return nil
}

// 对比两个handlerItem的优先级，需要非常注意的是，注意新老对比项的参数先后顺序
// 优先级比较规则：
// 1、层级越深优先级越高(对比/数量)；
// 2、模糊规则优先级：{xxx} > :xxx > *xxx；
func (s *Server) compareRouterPriority(newRouter, oldRouter *Router) bool {
    if newRouter.Priority > oldRouter.Priority {
        return true
    }
    if newRouter.Priority < oldRouter.Priority {
        return false
    }
    // 例如：/{user}/{act} 比 /:user/:act 优先级高
    if strings.Count(newRouter.Uri, "{") > strings.Count(oldRouter.Uri, "{") {
        return true
    }
    // 例如: /:name/update 比 /:name/:action优先级高
    if strings.Count(newRouter.Uri, "/:") < strings.Count(oldRouter.Uri, "/:") {
        // 例如: /:name/:action 比 /:name/*any 优先级高
        if strings.Count(newRouter.Uri, "/*") < strings.Count(oldRouter.Uri, "/*") {
            return true
        }
        return false
    }
    return false
}

// 将pattern（不带method和domain）解析成正则表达式匹配以及对应的query字符串
func (s *Server) patternToRegRule(rule string) (regrule string, names []string) {
    if len(rule) < 2 {
        return rule, nil
    }
    regrule = "^"
    array  := strings.Split(rule[1:], "/")
    for _, v := range array {
        if len(v) == 0 {
            continue
        }
        switch v[0] {
            case ':':
                regrule += `/([\w\.\-]+)`
                names    = append(names, v[1:])
            case '*':
                regrule += `/{0,1}(.*)`
                names    = append(names, v[1:])
            default:
                // 特殊字符替换
                v = gstr.ReplaceByMap(v, map[string]string{
                    `.` : `\.`,
                    `+` : `\+`,
                    `*` : `.*`,
                })
                s, _ := gregex.ReplaceStringFunc(`\{[\w\.\-]+\}`, v, func(s string) string {
                    names = append(names, s[1 : len(s) - 1])
                    return `([\w\.\-]+)`
                })
                if strings.EqualFold(s, v) {
                    regrule += "/" + v
                } else {
                    regrule += "/" + s
                }
        }
    }
    regrule += `$`
    return
}

