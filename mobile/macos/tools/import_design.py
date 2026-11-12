#!/usr/bin/env python3
"""Extract passive templates/data from the supplied offline design, never execute it."""
import ast, base64, gzip, hashlib, html, json, re, sys
from pathlib import Path

class Literal:
    def __init__(self, text, start=0): self.text, self.i = text, start
    def space(self):
        while self.i < len(self.text) and self.text[self.i].isspace(): self.i += 1
    def value(self):
        self.space(); s=self.text; c=s[self.i]
        if c in "'\"":
            start=self.i; self.i+=1
            while self.i<len(s):
                if s[self.i]=='\\': self.i+=2; continue
                if s[self.i]==c:
                    self.i+=1
                    return ast.literal_eval(s[start:self.i])
                self.i+=1
            raise ValueError('unterminated design string')
        if c in '[{':
            end=']' if c=='[' else '}'; self.i+=1; result=[] if c=='[' else {}
            while True:
                self.space()
                if s[self.i]==end: self.i+=1; return result
                if c=='{':
                    if s[self.i] in "'\"": key=self.value()
                    else:
                        m=re.match(r'[\w$]+',s[self.i:]); key=m[0];self.i+=len(key)
                    self.space(); assert s[self.i]==':';self.i+=1
                    result[key]=self.value()
                else: result.append(self.value())
                self.space()
                if s[self.i]==',': self.i+=1
                else: assert s[self.i]==end
        m=re.match(r'-?\d+(?:\.\d+)?|true|false|null',s[self.i:])
        if not m: raise ValueError('non-literal design data near '+s[self.i:self.i+50])
        self.i+=len(m[0]); return json.loads(m[0])

def packed(text, kind):
    return json.loads(re.search(r'<script type="__bundler/'+kind+r'">\s*(.*?)\s*</script>',text,re.S)[1])

def decode(item):
    b=base64.b64decode(item['data']); return gzip.decompress(b) if item.get('compressed') else b

source=Path(sys.argv[1]); output=Path(sys.argv[2]); output.mkdir(parents=True,exist_ok=True)
raw=source.read_text(); manifest=packed(raw,'manifest'); order=packed(raw,'page_order')
boards=[]; cards={}
for ident in order:
    inner=decode(manifest[ident]).decode(); template=packed(inner,'template')
    title=html.unescape(re.search(r'<title>(.*?)</title>',template,re.S)[1])
    script=re.search(r'<script type="text/x-dc"[^>]*>(.*?)</script>',template,re.S)
    sections=[]
    if script and 'sections: this.build(' in script[1]:
        text=script[1]; start=text.index('sections: this.build(')+len('sections: this.build(')
        sections=Literal(text,start).value()
        for section in sections:
            for card in section['cards']:
                cards[card[0]]={'id':card[0],'title':card[1],'blocks':card[2:],'board':ident}
    body=template[template.index('<x-dc>')+6:template.index('</x-dc>')]
    body=re.sub(r'<helmet>.*?</helmet>','',body,flags=re.S).strip()
    (output/(ident+'.html')).write_text(body)
    boards.append({'id':ident,'title':title,'sections':[{'name':x['name'],'note':x.get('note',''),'cards':[c[0] for c in x['cards']]} for x in sections]})
(output/'catalog.json').write_text(json.dumps({'boards':boards,'cards':cards},ensure_ascii=False,indent=2)+'\n')
(output/'provenance.json').write_text(json.dumps({'file':source.name,'sha256':hashlib.sha256(source.read_bytes()).hexdigest(),'boards':boards},ensure_ascii=False,indent=2)+'\n')
print(json.dumps({'boards':len(boards),'state_cards':len(cards)},ensure_ascii=False))
