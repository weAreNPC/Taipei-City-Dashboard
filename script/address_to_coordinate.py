import requests

BASE_URL = "https://geocode.arcgis.com/arcgis/rest/services/World/GeocodeServer/findAddressCandidates"

def get_coordinates(address):
    params = {
        'SingleLine': address,
        'f': 'json',
        'outSR': '{"wkid":4326}',
        'outFields': 'Addr_type,Match_addr,StAddr,City',
        'maxLocations': 6
    }
    
    response = requests.get(BASE_URL, params=params)
    
    if response.status_code == 200:
        data = response.json()
        if data['candidates']:
            return data['candidates'][0]['location']
        else:
            return None
    else:
        response.raise_for_status()


if __name__ == "__main__":
    address = "臺北市中正區信義路二段31號"
    coordinates = get_coordinates(address)
    
    print(coordinates)
