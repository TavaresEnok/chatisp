"""Dados sintéticos SOMENTE para a fixture M4A local em 127.0.0.1:28080."""
import hashlib, hmac, json, pathlib, time, urllib.request, uuid
f = json.loads(pathlib.Path('/home/enok/.local/share/chatisp/redesign-20261107/fixture.json').read_text())
assert f.get('M3B', {}).get('Actors'), 'Exige fixture descartável M3B/M4A'
for i, (name, text) in enumerate([
 ('SIMULAÇÃO · Marina Costa', 'Minha conexão está caindo desde cedo. Podem verificar?'),
 ('SIMULAÇÃO · Pedro Lima', 'Preciso da segunda via da minha fatura.'),
 ('SIMULAÇÃO · Julia Alves', 'Gostaria de mudar o endereço da instalação.'),
 ('SIMULAÇÃO · Carlos Mendes', 'Como acompanho a visita do técnico?'),
]):
 phone = '55119888870' + str(i).zfill(2)
 value = {'messaging_product':'whatsapp', 'metadata':{'phone_number_id':f['Phone'], 'display_phone_number':f['Phone']}, 'contacts':[{'wa_id':phone,'profile':{'name':name}}], 'messages':[{'from':phone,'id':'wamid.simulation.'+str(uuid.uuid4()),'timestamp':str(int(time.time())),'type':'text','text':{'body':text}}]}
 data = json.dumps({'object':'whatsapp_business_account','entry':[{'id':f['Waba'],'changes':[{'field':'messages','value':value}]}]}).encode()
 signature = hmac.new(f['Secret'].encode(),data,hashlib.sha256).hexdigest()
 request = urllib.request.Request('http://127.0.0.1:28080/api/webhooks/meta?app_id='+f['App'],data=data,headers={'Content-Type':'application/json','X-Hub-Signature-256':'sha256='+signature})
 with urllib.request.urlopen(request) as response:
  assert response.status == 200
print('Quatro mensagens recebidas pela API da bancada descartável.')
