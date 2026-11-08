#!/usr/bin/env python3
"""Deterministic app-server fixture. Never executes tools or changes user files."""
import json,sys,os
threads={}; pending={}; serial=0; loaded=set(); goals={}

# A persisted thread exists on disk but must be resumed in each new process.
if os.environ.get('QUICKGUI_FIXTURE_RESUME') == '1':
 threads['resume-thread']={'id':'resume-thread','name':'Persisted conversation','cwd':os.getcwd(),'preview':'Earlier reply','updatedAt':1700000000,'turns':[{'id':'old-turn','status':'completed','items':[{'type':'userMessage','id':'old-user','content':[{'type':'text','text':'Earlier prompt'}]},{'type':'agentMessage','id':'old-reply','text':'Earlier reply'}]}]}

# Opt-in native scroll fixture: populated history without a model request.
if os.environ.get('QUICKGUI_FIXTURE_SCROLL') == '1':
 for tid,title,short_tail in [('scroll-long','Scrollable history fixture',False),('scroll-short','Short tail history fixture',True)]:
  turns=[]
  for n in range(120):
   turn='scroll-turn-'+str(n)
   items=[{'type':'userMessage','id':turn+'-user','content':[{'type':'text','text':'SCROLL PROMPT %03d'%n}]},
          {'type':'agentMessage','id':turn+'-reply','text':'## SCROLL REPLY %03d\n\n'%n+'消息滚动检查：向上查看历史，向下回到最新内容。\n\n'*12}]
   if short_tail and n==119:
    items += [{'type':'commandExecution','id':'scroll-log-'+str(i),'command':'fixture log %03d'%i,'status':'completed','exitCode':0,'aggregatedOutput':'## hidden log\n\n**details**\n\n'*500} for i in range(100)]
   turns.append({'id':turn,'status':'completed','items':items})
  threads[tid]={'id':tid,'name':title,'preview':title,'cwd':os.getcwd(),'createdAt':1700000000,'updatedAt':1700000000,'turns':turns}

if os.environ.get('QUICKGUI_FIXTURE_RESUME_RUNNING') == '1' and 'resume-thread' in threads:
 threads['resume-thread']['turns'][0]['status']='inProgress'

def emit(v):
 print(json.dumps(v,ensure_ascii=False),flush=True)
def reply(req,result):emit({'id':req['id'],'result':result})
def notify(method,**params):emit({'method':method,'params':params})
def item(tid,turn,kind,i,**extra):
 value={'type':kind,'id':i,**extra}
 threads[tid]['turns'][-1]['items'].append(value)
 notify('item/started',threadId=tid,turnId=turn,item=value)
 notify('item/completed',threadId=tid,turnId=turn,item=value)
def session(p):
 mode=p.get('sandbox','workspace-write')
 sandbox={'danger-full-access':{'type':'dangerFullAccess'},'read-only':{'type':'readOnly','networkAccess':False}}.get(mode,{'type':'workspaceWrite','writableRoots':[],'networkAccess':False,'excludeTmpdirEnvVar':False,'excludeSlashTmp':False})
 forced=os.environ.get('QUICKGUI_FIXTURE_SANDBOX')
 if forced:sandbox=json.loads(forced)
 return {'sandbox':sandbox,'approvalPolicy':p.get('approvalPolicy','on-request'),'approvalsReviewer':p.get('approvalsReviewer','user'),'cwd':p.get('cwd',os.getcwd()),'model':'fixture','modelProvider':'fixture'}
def finish(tid,turn,text='Fixture interaction completed',status='completed'):
 item(tid,turn,'agentMessage',turn+'-final',text=text)
 threads[tid]['turns'][-1]['status']=status
 notify('turn/completed',threadId=tid,turn={'id':turn,'status':status})
for line in sys.stdin:
 try:r=json.loads(line)
 except ValueError:continue
 if 'method' not in r:
  key=str(r.get('id'));ctx=pending.pop(key,None)
  if ctx:
   path=os.environ.get('QUICKGUI_FIXTURE_LOG')
   if path:
    with open(path,'a') as log:log.write(json.dumps({'method':ctx[2],'response':r},ensure_ascii=False)+'\n')
   finish(ctx[0],ctx[1],'Server received: '+json.dumps(r.get('result',r.get('error')),ensure_ascii=False))
  continue
 method=r['method'];p=r.get('params') or {}
 path=os.environ.get('QUICKGUI_FIXTURE_LOG')
 if path:
  with open(path,'a') as log:log.write(json.dumps({'method':method,'params':p},ensure_ascii=False)+'\n')
 failure=os.environ.get({'thread/resume':'QUICKGUI_FIXTURE_RESUME_ERROR','turn/start':'QUICKGUI_FIXTURE_TURN_ERROR','thread/revert':'QUICKGUI_FIXTURE_REVERT_ERROR'}.get(method,''))
 if failure:
  emit({'id':r['id'],'error':json.loads(failure)});continue
 if method=='initialize':reply(r,{'userAgent':'quickgui-fixture/1'})
 elif method=='initialized':pass
 elif method=='model/list':reply(r,{'data':[{'id':'fixture','model':'fixture','displayName':'Fixture','isDefault':True,'defaultReasoningEffort':'low','supportedReasoningEfforts':[{'reasoningEffort':'low'},{'reasoningEffort':'high'}]}]})
 elif method=='thread/backgroundTerminals/list':reply(r,{'data':json.loads(os.environ.get('QUICKGUI_FIXTURE_BACKGROUND','{}')).get(p.get('threadId'),[]),'nextCursor':None})
 elif method=='config/read':reply(r,{'config':{'model':'fixture'}})
 elif method=='thread/list':
  reply(r,{'data':[{k:v for k,v in thread.items() if k!='turns'} for thread in threads.values() if thread['cwd']==p.get('cwd',os.getcwd())]})
 elif method=='thread/start':
  serial+=1;tid='fixture-'+str(serial);threads[tid]={'id':tid,'cwd':p.get('cwd',os.getcwd()),'preview':'Fixture thread','turns':[]};loaded.add(tid);reply(r,{'thread':threads[tid],**session(p)})
 elif method in ('thread/read','thread/resume'):
  tid=p['threadId']
  if tid not in threads:
   emit({'id':r['id'],'error':{'code':-32600,'message':'no rollout found for thread id '+tid}});continue
  if method=='thread/resume':loaded.add(tid);reply(r,{'thread':threads[tid],**session(p)})
  else:reply(r,{'thread':threads[tid]})
 elif method=='thread/revert':
  tid=p['threadId'];turns=threads[tid]['turns'];cut=next((i for i,turn in enumerate(turns) if turn['id']==p['beforeTurnId']),None)
  if cut is None:emit({'id':r['id'],'error':{'code':-32600,'message':'turn not found'}})
  else:threads[tid]['turns']=turns[:cut];reply(r,{'thread':threads[tid]})
 elif method=='thread/goal/get':reply(r,{'goal':goals.get(p['threadId'])})
 elif method=='thread/goal/set':
  tid=p['threadId'];goal=goals.setdefault(tid,{'threadId':tid,'objective':'','status':'active','tokensUsed':0,'timeUsedSeconds':0,'tokenBudget':None})
  for key in ('objective','status','tokenBudget'):
   if key in p:goal[key]=p[key]
  reply(r,{'goal':goal});notify('thread/goal/updated',threadId=tid,goal=goal)
 elif method=='thread/goal/clear':
  tid=p['threadId'];goals.pop(tid,None);reply(r,{});notify('thread/goal/cleared',threadId=tid)
 elif method=='turn/start':
  if p['threadId'] not in loaded:
   emit({'id':r['id'],'error':{'code':-32600,'message':'thread not found: '+p['threadId']}});continue
  serial+=1;tid=p['threadId'];turn='turn-'+str(serial);text=p['input'][0]['text'];threads[tid]['turns'].append({'id':turn,'status':'inProgress','items':[]});reply(r,{'turn':{'id':turn,'status':'inProgress'}});notify('turn/started',threadId=tid,turn={'id':turn,'status':'inProgress'});item(tid,turn,'userMessage',turn+'-user',content=p['input'])
  if text=='grouped':
   item(tid,turn,'agentMessage',turn+'-first',text='第一条回复：显示 Codex 图标。')
   item(tid,turn,'agentMessage',turn+'-second',text='第二条回复：省略重复图标。')
   finish(tid,turn,'第三条回复：继续省略图标。');continue
  if text=='bold-layout':
   finish(tid,turn,'按这个项目的实际范围看，**7.8 万明显偏低。**\n\n前面估算的重做成本约为 **60万～120万元**，生产级交付约 **100万～200万元**。你收取的 7.8 万，大约只相当于完整开发成本的 **4%～13%**。');continue
  if text=='markdown':
   import time
   content=open(os.path.join(os.path.dirname(__file__),'markdown.md')).read()
   notify('item/started',threadId=tid,turnId=turn,item={'id':turn+'-final','type':'agentMessage','text':''})
   for offset in range(0,len(content),24):
    notify('item/agentMessage/delta',threadId=tid,turnId=turn,itemId=turn+'-final',delta=content[offset:offset+24]);time.sleep(0.03)
   finish(tid,turn,content);continue
  if text=='disconnect':sys.exit(0)
  if text=='long':continue
  if text=='error':
   notify('error',threadId=tid,turnId=turn,willRetry=True,error={'message':'Fixture temporary error'})
   notify('turn/completed',threadId=tid,turn={'id':turn,'status':'failed','error':{'message':'Fixture permanent error'}});continue
  request_methods={'approval':'item/commandExecution/requestApproval','file':'item/fileChange/requestApproval','permissions':'item/permissions/requestApproval','question':'item/tool/requestUserInput','mcp':'mcpServer/elicitation/request'}
  if text in request_methods:
   serial+=1;request_id='request-'+str(serial);params={'threadId':tid,'turnId':turn,'itemId':turn+'-tool','cwd':os.getcwd(),'reason':'Synthetic validation only; no command will execute.'}
   if text=='approval':params['command']='echo fixture-only'
   if text=='file':item(tid,turn,'fileChange',turn+'-tool',status='inProgress',changes=[{'path':'example.txt','diff':'-old\n+new'}])
   if text=='permissions':params['permissions']={'network':{'enabled':True}}
   if text=='question':params.update({'isBlocking':True,'questions':[{'id':'color','header':'Color','question':'Choose a color','options':[{'label':'Blue','description':'Cool color'},{'label':'Red','description':'Warm color'}]},{'id':'name','header':'Name','question':'Type a name'}]})
   if text=='mcp':params.update({'serverName':'fixture','mode':'form','message':'Provide a label','requestedSchema':{'type':'object','required':['label'],'properties':{'label':{'type':'string'}}}})
   pending[request_id]=(tid,turn,request_methods[text]);emit({'id':request_id,'method':request_methods[text],'params':params});continue
  item(tid,turn,'reasoning','reason',summary=['Checking the fixture state'],content=['Do not display raw reasoning'])
  item(tid,turn,'plan','plan',text='1. Inspect\n2. Verify')
  notify('turn/plan/updated',threadId=tid,turnId=turn,plan=[{'step':'Inspect','status':'completed'},{'step':'Verify','status':'in_progress'}])
  notify('item/started',threadId=tid,turnId=turn,item={'id':'command','type':'commandExecution','command':'echo synthetic','status':'inProgress'})
  notify('item/commandExecution/outputDelta',threadId=tid,turnId=turn,itemId='command',delta='synthetic output\n')
  item(tid,turn,'commandExecution','command',command='echo synthetic',status='completed',exitCode=0,aggregatedOutput='synthetic output\n')
  item(tid,turn,'fileChange','file',status='completed',changes=[{'path':'example.go','diff':'-before\n+after'}])
  notify('turn/diff/updated',threadId=tid,turnId=turn,diff='--- example.go\n+++ example.go\n-before\n+after')
  item(tid,turn,'mcpToolCall','mcp',server='fixture',tool='lookup',status='completed',result={'value':'synthetic'})
  item(tid,turn,'dynamicToolCall','dynamic',tool='custom',status='failed',error={'message':'Fixture tool failure'})
  item(tid,turn,'collabAgentToolCall','agent',tool='wait',status='completed',agentsStates={'child':{'status':'completed'}})
  item(tid,turn,'webSearch','search',query='fixture search')
  item(tid,turn,'imageView','image',path='/tmp/fixture.png')
  item(tid,turn,'imageGeneration','generated',status='completed',savedPath='/tmp/generated.png')
  item(tid,turn,'contextCompaction','compact')
  item(tid,turn,'enteredReviewMode','review',review='Review started')
  item(tid,turn,'exitedReviewMode','review-end',review='No findings')
  notify('thread/tokenUsage/updated',threadId=tid,turnId=turn,tokenUsage={'last':{'totalTokens':123}})
  notify('item/agentMessage/delta',threadId=tid,turnId=turn,itemId='final',delta='Hello ')
  notify('item/agentMessage/delta',threadId=tid,turnId=turn,itemId='final',delta='世界')
  item(tid,turn,'agentMessage','final',text='Hello 世界 — final authoritative text.')
  notify('turn/completed',threadId=tid,turn={'id':turn,'status':'completed'})
 elif method=='turn/steer':
  serial+=1;tid=p['threadId'];turn=p['expectedTurnId']
  reply(r,{})
  item(tid,turn,'userMessage','steer-'+str(serial),content=p['input'])
 elif method in ('review/start','thread/compact/start'):
  serial+=1;tid=p['threadId'];turn='turn-'+str(serial)
  threads[tid]['turns'].append({'id':turn,'status':'inProgress','items':[]})
  reply(r,{'turn':{'id':turn,'status':'inProgress'}} if method=='review/start' else {})
  notify('turn/started',threadId=tid,turn={'id':turn,'status':'inProgress'})
  finish(tid,turn,'Fixture received '+method)
 elif method=='turn/interrupt':
  reply(r,{})
  for key,ctx in list(pending.items()):
   if ctx[:2]==(p['threadId'],p['turnId']):pending.pop(key);notify('serverRequest/resolved',threadId=p['threadId'],requestId=key)
  finish(p['threadId'],p['turnId'],'Interrupted by user',status='interrupted')
 else:emit({'id':r.get('id'),'error':{'code':-32601,'message':'Unsupported fixture method '+method}})
