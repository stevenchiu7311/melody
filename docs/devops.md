# Deployment with TravisCI
### Installation
```
stevenchiu@CM-PC-02034:~/cmgs$ sudo apt-get install ruby-dev
stevenchiu@CM-PC-02034:~/cmgs$ sudo gem install travis -v 1.8.9 --no-rdoc --no-ri
```
## Github release
### 不加密 sensitive data 
```
stevenchiu@CM-PC-02034:~/melody$ sudo travis setup releases
Username: stevenchiu7311
Password for stevenchiu7311: **********
File to Upload:
Deploy only from stevenchiu7311/melody? |yes| yes
Encrypt API key? |yes| no
```
會在 
[Developer settings](https://github.com/settings/developers) => [Personal access tokens](https://github.com/settings/tokens)
底下看到 
**[automatic releases for stevenchiu7311/melody](https://github.com/settings/tokens/269591027)** _—  public_repo_
的產生，如果重複執行指令，這筆 key 就會被覆蓋新的

不過在 .travis 檔裡面這筆 api token 變因此暴露出來，如下
```
deploy:
  provider: releases
  api_key: ce23d3058032f492ccacff92f15fecd737900617
  file: ''
  on:
    repo: stevenchiu7311/melody
``` 

要解決此問題需要把 api token 加在 Travis 後台的環境變數中

![enter image description here](https://s3-ap-northeast-1.amazonaws.com/steven-dev/presentation/travis/env.PNG)

然後在 .travis 中使用此環境變數取帶原本暴露的 api token
`all_branches: true` 此為宣告讓 master 以外的分支也能允許 deploy stage 的設定

```
deploy:
  provider: releases
  api_key:
    secure: $API_KEY_SECURE
  file_glob: true
  file: 'reports/*'
  skip_cleanup: true
  on:
    repo: stevenchiu7311/melody
    all_branches: true
```

### 加密 sensitive data 
```
stevenchiu@CM-PC-02034:~/melody$ sudo travis setup releases
Username: stevenchiu7311
Password for stevenchiu7311: **********
File to Upload:
Deploy only from stevenchiu7311/melody? |yes| yes
Encrypt API key? |yes| yes
```
會在 .travis 中看到一個 api token 被加密過後的字串
```
deploy:
  provider: releases
  api_key:
    secure: KcVFyJdx20Qp7ZIFbdBjkPTl3Lg6q6TP6C3MDEDME01SweWGNZKckK4OfVOgctLNCZmZqJVxoRErzTrzAQiqpMuL1eKvN1g+QsWwukaR6U0dM52UwHv6vBmCbtTxVfPP3b5bPlDVSCJwYE75A0yR21oa2HXggp7yjkCa1wah8UOmjsZnESWBvs/6KmdPtTJyybv0FccGRdQ28+NYrT99QiDzMD6NYFJpdpKiQDF8eTzIgQU+UpL3uz0MZvvqwOniEhbRLYZc2b6uvEKttuFFGEPMpjwTJcVqU9KxchivtzQU/jgIeUqdiqhwiPNPqVu7W/syr/h+2c5Mt3ao5tbK+0NARSBijYh3AxUztQa9pQ3YQwmlJUDm3sabJenbQ3PMnjllRSn0HaqkAEZ8rY0tGX5Vc0BC8uw2Kyy4dBMc0P8+E7GHHXisL+Gq1I9o+lBAbTwb0I70ogJXvsyvh7+635ejgqtiG6GLP9ntx/8FPRZXBXVsaiEaY0KeTS4NzfpdpTvphSPHxdcN+lJNnmb5REvC3h26NJSvfYQbZIcfRuuWUXd9UGEGhwvBUkPIjQlBx/PKSo+KwCGf+8bjcAAJKHaVHQH8BvyIAC3QPRvMNnDvsV0QNvAEtq+5CVWbQlZ92AHoAL+bSQmLPQnHc4hn0o8OayDLgP0unB9eKlJLYck=
  file: ''
  on:
    repo: stevenchiu7311/melody
```

這個 secure 後面的字串是用 travis 提供的公鑰加密的，從而只有 travis 能解密看到 secure 裏的內容，別人即使看到了 travis.yml 的 secure 也沒法解密得到我的 token ，從而不可能代替我操作 travis ，所以放在 .travis 裡暴露出來也沒關係。我想這也跟實驗這個 secure 字串用環境變數的方式設定在後台卻沒有作用相呼應吧。﹝可以在 build 流程 echo 出來但是 deploy 時仍然顯示錯誤﹞

## Github pages

先建立一個空內容的 branch gh-pages，此為容納佈署內容的分支
```
stevenchiu@CM-PC-02034:~/melody$ git checkout --orphan gh-pages
stevenchiu@CM-PC-02034:~/melody$ touch index.html
stevenchiu@CM-PC-02034:~/melody$ git add index.html
stevenchiu@CM-PC-02034:~/melody$ git commit -m 'first gh-pages commit'
```

以下範例為用 api_key 加密方式認證來同時佈署到 github repository 以及發佈到 github release
```
deploy:
  - provider: releases
    api_key:
      secure: KcVFyJdx20Qp7ZIFbdBjkPTl3Lg6q6TP6C3MDEDME01SweWGNZKckK4OfVOgctLNCZmZqJVxoRErzTrzAQiqpMuL1eKvN1g+QsWwukaR6U0dM52UwHv6vBmCbtTxVfPP3b5bPlDVSCJwYE75A0yR21oa2HXggp7yjkCa1wah8UOmjsZnESWBvs/6KmdPtTJyybv0FccGRdQ28+NYrT99QiDzMD6NYFJpdpKiQDF8eTzIgQU+UpL3uz0MZvvqwOniEhbRLYZc2b6uvEKttuFFGEPMpjwTJcVqU9KxchivtzQU/jgIeUqdiqhwiPNPqVu7W/syr/h+2c5Mt3ao5tbK+0NARSBijYh3AxUztQa9pQ3YQwmlJUDm3sabJenbQ3PMnjllRSn0HaqkAEZ8rY0tGX5Vc0BC8uw2Kyy4dBMc0P8+E7GHHXisL+Gq1I9o+lBAbTwb0I70ogJXvsyvh7+635ejgqtiG6GLP9ntx/8FPRZXBXVsaiEaY0KeTS4NzfpdpTvphSPHxdcN+lJNnmb5REvC3h26NJSvfYQbZIcfRuuWUXd9UGEGhwvBUkPIjQlBx/PKSo+KwCGf+8bjcAAJKHaVHQH8BvyIAC3QPRvMNnDvsV0QNvAEtq+5CVWbQlZ92AHoAL+bSQmLPQnHc4hn0o8OayDLgP0unB9eKlJLYck=
    file_glob: true
    file: 'reports/*'
    skip_cleanup: true
    on:
      repo: stevenchiu7311/melody
      all_branches: true
  - provider: pages
    local-dir: 'reports'
    skip-cleanup: true
    github-token:
      secure: KcVFyJdx20Qp7ZIFbdBjkPTl3Lg6q6TP6C3MDEDME01SweWGNZKckK4OfVOgctLNCZmZqJVxoRErzTrzAQiqpMuL1eKvN1g+QsWwukaR6U0dM52UwHv6vBmCbtTxVfPP3b5bPlDVSCJwYE75A0yR21oa2HXggp7yjkCa1wah8UOmjsZnESWBvs/6KmdPtTJyybv0FccGRdQ28+NYrT99QiDzMD6NYFJpdpKiQDF8eTzIgQU+UpL3uz0MZvvqwOniEhbRLYZc2b6uvEKttuFFGEPMpjwTJcVqU9KxchivtzQU/jgIeUqdiqhwiPNPqVu7W/syr/h+2c5Mt3ao5tbK+0NARSBijYh3AxUztQa9pQ3YQwmlJUDm3sabJenbQ3PMnjllRSn0HaqkAEZ8rY0tGX5Vc0BC8uw2Kyy4dBMc0P8+E7GHHXisL+Gq1I9o+lBAbTwb0I70ogJXvsyvh7+635ejgqtiG6GLP9ntx/8FPRZXBXVsaiEaY0KeTS4NzfpdpTvphSPHxdcN+lJNnmb5REvC3h26NJSvfYQbZIcfRuuWUXd9UGEGhwvBUkPIjQlBx/PKSo+KwCGf+8bjcAAJKHaVHQH8BvyIAC3QPRvMNnDvsV0QNvAEtq+5CVWbQlZ92AHoAL+bSQmLPQnHc4hn0o8OayDLgP0unB9eKlJLYck=
    keep-history: true
    on:
      repo: stevenchiu7311/melody
      all_branches: true
```

 接著就可以在 `https://stevenchiu7311.github.io/melody/coverage.html` 看到佈署的頁面